// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// What the app says about push, in one closed set of phrases
// (mobile plan, Task P5.2.8; the design is
// docs/mobile/design/push-notifications.md §3.1, §3.8 and §6).
//
// **Why the sentences are here and not in a screen.** Two facts about push are
// shown to the owner -- whether this phone is registered, and what the switches
// are -- and both are rendered from state this directory owns: an outcome
// (`registration.js`) and the prefs (`controls.js`). A screen that wrote its own
// sentence for `no-token` would be a second answer to "what does the app say
// when it has no token", and the one that matters is the one the *test* pins.
// So the phrases are a table here, keyed by the state and the code the outcome
// carries, and a screen renders the table.
//
// **No phrase names a repository, a run, an amount or a code.** §3.1's rule for
// an alert is a rule about push's surface generally: the app's own words name
// push and nothing about the owner's work, so nothing here can be turned into a
// lure that reads as state (§4 rules 1 and 3). A code is a word for the app,
// never for the owner: an outcome this table does not recognise gets the
// waiting phrase rather than its code, which is also what keeps a refusal from
// an unbuilt path from being displayed as a sentence nobody wrote.
//
// **The switches are labelled by §1's own categories** (D160's set), one line
// each, so a switch the design adds shows up as a name this table is missing
// rather than as a blank row (`pushScreen` answers `null` for it).

import { CONTROL_DEFAULTS, CONTROL_NAMES } from './controls.js';
import { PUSH_STATES } from './registration.js';

/**
 * The phrase for each STATE (`never`, `registered`) and for each refusal code
 * that gets one of its own. The two vocabularies live in one table because a
 * screen branches on one of them and never on which kind it was handed:
 * `pushStateLine` looks the state up first and the code second.
 */
export const PUSH_LINES = Object.freeze({
  [PUSH_STATES.REGISTERED]: 'This phone is registered for push notifications.',
  [PUSH_STATES.NEVER]: 'Push notifications have not been set up on this phone yet.',
  /** The phone has no token to register with -- §6's first row, said out loud. */
  'no-token': 'This phone has no push token yet, so nothing has been sent. The app will try again.',
  'token-failed': 'The app could not ask this phone for a push token, so nothing has been sent.',
  'relay-failed': 'The app could not register this phone with the push service, so nothing has been sent.',
  'no-connection': 'This phone is offline, so the registration is waiting for a connection.',
});

/**
 * What everything else reads as: a store failure, a lapse, a schema refusal, a
 * code this table has never heard of. The owner's next step is the same for
 * all of them -- open the app again -- so they say the same thing, and a code
 * that is not a phrase the app wrote is never shown as one.
 */
export const PUSH_WAITING = 'The registration has not been sent yet. The app will try again next time it opens.';

/** The five switches, in §1's own words. */
export const CONTROL_LINES = Object.freeze({
  kill_switch: 'The kill switch',
  cap_reached: 'A cap is reached',
  consumer_offline: 'The orchestrator is offline',
  cap_near: 'A cap is nearly reached',
  prompt: 'A question is waiting for an answer',
});

/** A minute of the local day as a clock time: 1320 -> `22:00`. */
function clockText(minute) {
  const hours = String(Math.floor(minute / 60)).padStart(2, '0');
  const minutes = String(minute % 60).padStart(2, '0');
  return `${hours}:${minutes}`;
}

/**
 * The sentence for one registration outcome -- the state's phrase, or the
 * code's, or the waiting phrase.
 *
 * @param {object} outcome what `register()` or `state()` answered
 * @returns {string}
 */
export function pushStateLine(outcome) {
  const state = outcome === null || typeof outcome !== 'object' ? PUSH_STATES.NEVER : outcome.state;
  if (state === PUSH_STATES.REGISTERED) return PUSH_LINES[PUSH_STATES.REGISTERED];
  if (state === PUSH_STATES.NEVER) return PUSH_LINES[PUSH_STATES.NEVER];
  const code = outcome.code;
  if (typeof code === 'string' && Object.hasOwn(PUSH_LINES, code)) return PUSH_LINES[code];
  return PUSH_WAITING;
}

/**
 * The screen's whole state: what to say about the registration, the five
 * switches as they will travel, and the quiet-hours window (or that there is
 * none).
 *
 * `prefs` is `controls.read()`'s record, or absent -- a screen that has not
 * read one yet shows D160's defaults rather than an empty list, which is what
 * the phone will send if the owner never touches a switch.
 *
 * @param {object} [options]
 * @param {object} [options.outcome] the last registration outcome
 * @param {object} [options.prefs] the switches
 * @returns {object} the frozen render
 */
export function pushScreen({ outcome, prefs } = {}) {
  const switches = prefs === null || typeof prefs !== 'object' ? CONTROL_DEFAULTS : prefs;
  const window = switches.quiet_hours;
  const quietHours = window === null || typeof window !== 'object'
    ? Object.freeze({ on: false, text: 'Quiet hours are off.' })
    : Object.freeze({
      on: true,
      zone: window.zone,
      text: `Quiet hours are ${clockText(window.start)} to ${clockText(window.end)} (${window.zone}).`,
    });
  return Object.freeze({
    state: Object.freeze({
      text: pushStateLine(outcome),
      ok: outcome !== null && typeof outcome === 'object' && outcome.state === PUSH_STATES.REGISTERED,
    }),
    controls: Object.freeze(CONTROL_NAMES.map((name) => Object.freeze({
      name,
      text: Object.hasOwn(CONTROL_LINES, name) ? CONTROL_LINES[name] : null,
      on: switches[name] === true,
    }))),
    quietHours,
  });
}
