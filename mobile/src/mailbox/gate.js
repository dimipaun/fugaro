// The offline gate (mobile plan, Task P6.10; D167, G21).
//
// One pure function over two observations -- whether there is a connection, and
// what the last look at the consumer's heartbeat said -- answering whether the
// phone may send, and if not, which of the reasons it is. Nothing here writes,
// speaks or reads anything: the gate decides, `client.js` acts on the decision,
// and the sentences the owner hears live in `OFFLINE_SPEECH` below so that the
// voice screen (P3.5/P6.12) and this module cannot drift into saying two
// different things about one refusal.
//
// **What D167 decided, and why this is a module rather than two `if`s.** G21 in
// `docs/mobile/functional-spec-reconciliation.md` names the hazard: the
// Firebase SDK queues a write made while the device is offline and sends it on
// reconnect, so a command spoken in a tunnel runs an hour later, or is refused
// at the other end as a replay and never runs at all -- and the owner is told
// neither. D167's answer (the owner's, 2026-10-17) is that the app **never
// relies on that queue**: with no connection, or with the consumer stale or
// offline, it **refuses to send**, says why, and keeps the utterance only as
// unsent text the owner can resend; a destructive action record is never queued
// at all. Those are three separate rules and each has its own test in
// `gate.test.js` and in `client.test.js`.
//
// **Presence is an observation, not a guess, and an unobserved consumer is not
// a refusal.** The second input is the presence object a poll produced
// (`client.js`'s `presenceOf`, over P1.4's rule). A phone that has not polled
// yet has observed nothing: `presence === null` is not evidence that the
// consumer is stale or offline, and inventing a refusal from it would make a
// freshly paired phone unable to send its first sentence. The gate is a gate on
// what the phone has SEEN, and this is the one place the plan's two conditions
// ("no connection, or the consumer stale/offline") do not cover. The residual
// risk is bounded by what already exists -- the consumer's own replay window
// (P1.1, six hours for a command) and the mailbox's retention TTL (D118, 72
// hours) -- and it is named in `README.md` rather than left to be found.
//
// **A state that is not `live` is not a licence to send.** `clock-differs` is
// P1.4's fourth state and P1.4 is explicit that it is never reported as
// offline; it means the phone's clock does not agree with the server's, which is
// the one thing that makes the `at` this phone stamps untrustworthy -- and the
// consumer (P6.7) refuses a record stamped more than five minutes ahead of its
// own clock. So it refuses, with its own code, rather than being folded into
// `consumer-offline`: the owner is told the true thing, and a refusal code is
// what a screen switches on.

import { STATE } from '../../../shared/presence/index.js';

/**
 * The refusal codes this gate adds to the client's. One per distinct reason,
 * because "the phone is in a tunnel" and "the orchestrator has stopped
 * answering" are different facts and a single code for both loses the second.
 */
export const GATE_CODES = Object.freeze({
  /** The device has no connection: the SDK would hold the write, not send it. */
  NO_CONNECTION: 'no-connection',
  /** The consumer has missed its intervals (P1.4's `stale`). */
  CONSUMER_STALE: 'consumer-stale',
  /** The consumer is silent, unverified or unknown (P1.4's `offline`). */
  CONSUMER_OFFLINE: 'consumer-offline',
  /** The phone's clock does not agree with the server's (P1.4's `clock-differs`). */
  CONSUMER_CLOCK_DIFFERS: 'consumer-clock-differs',
});

/**
 * What the owner is told, one sentence per refusal code.
 *
 * It is a table rather than a branch inside `client.js` because two screens
 * render it (P6.9's text screen and P3.5/P6.12's voice screen) and a sentence
 * written twice is a sentence that ends up disagreeing with itself. Each one
 * says the two things D167 asks for: that nothing was sent, and why.
 */
export const OFFLINE_SPEECH = Object.freeze({
  [GATE_CODES.NO_CONNECTION]: 'No connection. I have not sent that.',
  [GATE_CODES.CONSUMER_STALE]: 'The orchestrator has gone quiet. I have not sent that.',
  [GATE_CODES.CONSUMER_OFFLINE]: 'The orchestrator is offline. I have not sent that.',
  [GATE_CODES.CONSUMER_CLOCK_DIFFERS]: 'This phone\'s clock does not match the server\'s. I have not sent that.',
});

/**
 * The sentence for one gate refusal, or `null` for a code this table does not
 * carry -- `null` rather than a default sentence, because a caller that shows a
 * generic sentence for a reason nobody wrote is a caller telling the owner
 * something nobody decided.
 *
 * @param {unknown} code one of `GATE_CODES`
 * @returns {string|null}
 */
export function offlineSpeech(code) {
  return Object.hasOwn(OFFLINE_SPEECH, code) ? OFFLINE_SPEECH[code] : null;
}

function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * May the phone send?
 *
 * @param {object} inputs
 * @param {unknown} inputs.online what the connection port answered. **Anything
 *   that is not `true` is offline**: a port that is missing, wired wrong or
 *   throwing its state away must refuse to send rather than send into the void.
 * @param {object|null} [inputs.presence] the last poll's presence object
 *   (`{state, verified, reason, ageMs, agents}`), or `null` when no poll has
 *   run. Only `state` and `verified` are read.
 * @returns {{ok: true} | {ok: false, code: string, field: string}} frozen
 */
export function evaluateGate({ online, presence = null } = {}) {
  if (online !== true) return refuse(GATE_CODES.NO_CONNECTION, 'connection');
  if (presence === null || presence === undefined) return Object.freeze({ ok: true });
  if (typeof presence !== 'object' || Array.isArray(presence)) {
    // Not a presence object at all: nothing about it is evidence that the
    // consumer is running, so it is read the way every other unusable answer is.
    return refuse(GATE_CODES.CONSUMER_OFFLINE, 'presence');
  }
  // `verified` is part of the question rather than a separate one: P1.4's rule
  // says nothing about whether the heartbeat was the consumer's, and the client
  // already reports an unverified heartbeat as `offline` -- so a presence a
  // caller built by hand, with the flag missing, is not a live consumer either.
  if (presence.state === STATE.LIVE && presence.verified === true) return Object.freeze({ ok: true });
  switch (presence.state) {
    case STATE.STALE:
      return refuse(GATE_CODES.CONSUMER_STALE, 'presence');
    case STATE.CLOCK_DIFFERS:
      return refuse(GATE_CODES.CONSUMER_CLOCK_DIFFERS, 'presence');
    case STATE.OFFLINE:
      return refuse(GATE_CODES.CONSUMER_OFFLINE, 'presence');
    default:
      // A state this module does not know -- including a `live` that is not
      // verified, handled above. Fail closed, and say the honest thing: nothing
      // here knows that the consumer is running.
      return refuse(GATE_CODES.CONSUMER_OFFLINE, 'presence');
  }
}
