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
// **Nothing observed is not a licence to send either, and this is the review's
// M2.** The second input is the presence object a poll produced (`client.js`'s
// `presenceOf`, over P1.4's rule), and `null` -- no poll has looked yet -- is
// the absence of an observation. The first version of this file read that as
// "not a refusal", on the argument that refusing would leave a freshly paired
// phone unable to say its first sentence. **The gate fails closed instead**: an
// unobserved consumer is treated exactly like an offline one, because a gate
// that sends on "probably fine" is the gate G21 is about, and D167 is
// SECURITY-SENSITIVE precisely so that the doubtful case is the refusing one.
// The cost is real and is the whole of the price: a phone must have completed
// one poll before it can speak, which is the state the app is in within one
// heartbeat interval of pairing -- and `poll` is a read, so it needs no
// permission the phone does not already have. It gets its own code
// (`consumer-unobserved`) rather than being folded into `consumer-offline`,
// because "I have not heard from it yet" and "it has stopped answering" are
// different things to say to the owner and a screen should not have to guess
// which one it is showing.
//
// **A state that is not `live` is not a licence to send.** `clock-differs` is
// P1.4's fourth state and P1.4 is explicit that it is never reported as offline;
// it means the phone's clock does not agree with the consumer's -- and the
// direction is worth stating, because the obvious reading of it is backwards.
// P1.4 produces the state when `ageMs < -futureToleranceMs`, and `ageMs` is
// `now - at`: the heartbeat's `at` is AHEAD of this phone's `now`, so the
// phone's clock is BEHIND, and the `at` it will stamp is EARLY rather than late.
// P6.7's "more than five minutes ahead of the consumer's clock" is therefore
// **not** the rule that would catch this record -- it is the rule for a clock
// running fast. A stamp that is early is caught by nothing at the other end
// either: it only makes the record look older than it is, and every window that
// judges it (P1.1's replay window, D118's TTL, P6.7's confirmation timeout) is
// measured on the CONSUMER's clock, so the age the phone believes it stamped is
// not the age the consumer will compute. The phone cannot fix that by stamping
// anyway: D117 makes the server's timestamp the authority, and this is the state
// that says the two ends disagree about what time it is. So it refuses, with its
// own code, rather than being folded into `consumer-offline`: the owner is told
// the true thing, and a refusal code is what a screen switches on.

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
  /**
   * No poll has looked yet, so there is nothing to judge (the review's M2).
   * It is a refusal and not a default: the phone may not send to a consumer it
   * has never seen.
   */
  CONSUMER_UNOBSERVED: 'consumer-unobserved',
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
  [GATE_CODES.CONSUMER_UNOBSERVED]: 'I have not heard from the orchestrator yet. I have not sent that.',
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
 * The connection half of the gate, **on its own**, which is what `flush` and
 * `poll` ask.
 *
 * It is a function rather than a flag on `evaluateGate` because the two halves
 * are asked by different callers for different reasons, and because M2 made the
 * difference load-bearing: once "no observation" is a refusal, a caller that
 * only means to ask "is there a connection" (a re-send, a read) must not be
 * silently asking the presence question too and refusing with
 * `consumer-unobserved` -- which is exactly what happened when `flush` and
 * `poll` called `evaluateGate({online})` and the default presence became a
 * refusal. Presence is what a poll OBSERVES; a poll that refused to look until
 * it had an observation would never make one.
 *
 * @param {unknown} online what the connection port answered; anything that is
 *   not `true` is offline
 * @returns {{ok: true} | {ok: false, code: string, field: string}} frozen
 */
export function evaluateConnection(online) {
  if (online !== true) return refuse(GATE_CODES.NO_CONNECTION, 'connection');
  return Object.freeze({ ok: true });
}

/**
 * May the phone send?
 *
 * @param {object} inputs
 * @param {unknown} inputs.online what the connection port answered. **Anything
 *   that is not `true` is offline**: a port that is missing, wired wrong or
 *   throwing its state away must refuse to send rather than send into the void.
 * @param {object|null} [inputs.presence] the last poll's presence object
 *   (`{state, verified, reason, ageMs, agents}`), or `null`/`undefined` when no
 *   poll has run -- which is a refusal (M2). Only `state` and `verified` are
 *   read.
 * @returns {{ok: true} | {ok: false, code: string, field: string}} frozen
 */
export function evaluateGate({ online, presence = null } = {}) {
  const connected = evaluateConnection(online);
  if (!connected.ok) return connected;
  if (presence === null || presence === undefined) {
    return refuse(GATE_CODES.CONSUMER_UNOBSERVED, 'presence');
  }
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
