// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The voice screen: the interrupt switch, the queue it gates, the offline
// refusal and the lease (mobile plan, Task P3.5; D122, D127, D126, D118).
//
// **What this module is.** P3.4 shipped the audio interfaces and their fakes;
// P3.6 shipped the mailbox client, the app's only writer; P1.2, P1.3 and P1.4
// shipped the lease, the severity ladder and the presence rule. This is the
// screen those five are wired into, and it owns exactly four decisions:
//
//   1. **the interrupt switch (D127)** -- a local phone setting, never written
//      to the database, gating whether a proactive update may be spoken now
//      (`interrupt.js` holds the rule; this file holds the queue it drains);
//   2. **the offline refusal** -- P1.4's `offline` is shown, and the app
//      **refuses to accept a message** while it holds. The spec's own sentence
//      is *rather than accepting messages into the void*, and the refusal is
//      one line here because the alternative -- a message the user believes
//      they sent, in a mailbox nobody is reading -- is the failure it names;
//   3. **the lease** -- P1.2's claim, taken and released, with a lease another
//      consumer holds **surfaced rather than ignored**;
//   4. **D122's degradation** -- the OS refusing the microphone is shown, so
//      the "tap to speak" control has something to be built on.
//
// **What it deliberately does not do.** No engine (D123/D46: on-device only,
// chosen by a bake-off -- owner-run check 85), no spoken confirmation (P6.12,
// which builds on this task later: the seam is `accept` and the pipeline's
// events, and none of it is implemented here), no prompt queue (P6.5, which is
// not a dependency of this task -- see `interrupt.js` on the one place the two
// disagree), and no writing of its own: **every** write goes through the client
// (`accept` calls `client.send` and nothing else), which is what keeps D113's
// "one write path" true.
//
// **The queue is bounded and refuses rather than evicting.** A queue that
// dropped its oldest entry to make room would lose an update silently, which is
// the same failure as speaking over the user with a different name. It is also
// the reason the queue holds updates that are below the digest's top: they are
// waiting for a boundary the ladder allows, and the plan's sentence -- *the
// digest's highest severity is the only thing that interrupts* -- is about what
// is spoken, not about what is remembered.
//
// **The lease is surfaced and not enforced.** P3.5's sentence is *a lease held
// by another consumer is surfaced rather than ignored*, and this file does
// exactly that and no more: the refusal is returned with P1.2's `existing`
// claim, the holder is in the snapshot a screen renders, and **nothing is
// gated on it**. Gating the phone's sending on a lease would let a laptop that
// died mid-run silence the owner's phone -- a failure mode worse than the one
// it prevents, and a policy the plan does not ask for.

import { SEVERITY_ORDER } from '../../../shared/digest/index.js';
import { emptyLeaseState, readLease, releaseLease as release, takeLease as take } from '../../../shared/lease/index.js';
import { STATE } from '../../../shared/presence/index.js';
import { isVoicePipeline } from '../audio/index.js';
import { INTERRUPT_SWITCH, interruptDecision } from './interrupt.js';

/** The refusal codes this module answers with. */
export const SCREEN_CODES = Object.freeze({
  /** The switch is neither `off` nor `on`. */
  BAD_SWITCH: 'bad-switch',
  /** A presence that is not P1.4's answer: not an object, or no known state. */
  BAD_PRESENCE: 'bad-presence',
  /** A digest that is not a digest: not an object. */
  BAD_DIGEST: 'bad-digest',
  /** A severity that is not on P1.3's ladder. */
  BAD_SEVERITY: 'bad-severity',
  /** An update that is not an object. */
  BAD_UPDATE: 'bad-update',
  /** An update with no text, or a message with none. */
  BAD_TEXT: 'bad-text',
  /** An update carrying a field an update does not have. */
  UNKNOWN_FIELD: 'unknown-field',
  /** No digest has arrived, so nothing can be judged against one. */
  NO_DIGEST: 'no-digest',
  /** The queue is full: refused rather than evicted. */
  QUEUE_FULL: 'queue-full',
  /** A lease state P1.2 will not read. */
  BAD_LEASE: 'bad-lease',
  /**
   * The app will not accept a message while P1.4 says `offline`. The code is
   * the state's own word, so a screen can render it without a second table.
   */
  OFFLINE: 'offline',
});

/**
 * How many proactive updates may wait at once. Bounded because a queue that
 * grows without limit is a memory leak with a nice name; refused rather than
 * evicted because an update dropped to make room is an update lost silently.
 */
export const QUEUE_CAP = 32;

/** The fields an update has, exactly. */
const UPDATE_FIELDS = Object.freeze(['text', 'severity']);

const PRESENCE_STATES = Object.freeze(Object.values(STATE));

/** A frozen refusal, in the shape this repository's modules use. */
function refuse(code, field = null) {
  return Object.freeze({ ok: false, code, field });
}

/** A frozen list, for the values a snapshot hands out. */
function freezeList(items) {
  return Object.freeze([...items]);
}

/**
 * Build the voice screen.
 *
 * Everything is a port or a value: the pipeline (P3.4), the mailbox client
 * (P3.6), the server-anchored clock (D117) and this installation's holder name
 * (P1.2). A missing or wrongly-shaped one **throws**, because a screen built
 * without them is a screen that fails at the first thing the owner says -- the
 * rule `createVoicePipeline` and `createMailboxClient` both hold.
 *
 * @param {object} options
 * @param {object} options.pipeline the audio pipeline (`isVoicePipeline`)
 * @param {object} options.client the mailbox client: `send(text)`, `poll()`
 * @param {() => number} options.now D117's server-anchored now, in ms
 * @param {string} options.holder this installation's name, for P1.2's lease
 * @param {string} [options.switch] D127's setting; `off` unless the owner set it
 * @param {number} [options.queueCap] how many updates may wait; `QUEUE_CAP`
 * @param {(text: string) => Promise<object>} [options.utterance] P6.12's spoken
 *   dialogue, which routes a final transcript instead of accepting it as a
 *   message; absent means every final is a message
 * @returns {object} the screen
 * @throws {TypeError} for a port, a value source or a setting that cannot be used
 */
export function createVoiceScreen({
  pipeline,
  client,
  now,
  holder,
  switch: setting = INTERRUPT_SWITCH.OFF,
  queueCap = QUEUE_CAP,
  utterance: dialogue = null,
} = {}) {
  if (!isVoicePipeline(pipeline)) throw new TypeError('pipeline is a VoicePipeline');
  if (client === null || typeof client !== 'object'
    || typeof client.send !== 'function' || typeof client.poll !== 'function') {
    throw new TypeError('client is a mailbox client: send(text) and poll()');
  }
  if (typeof now !== 'function') throw new TypeError('now is a function');
  if (typeof holder !== 'string' || holder.length === 0) throw new TypeError('holder is a non-empty string');
  if (setting !== INTERRUPT_SWITCH.OFF && setting !== INTERRUPT_SWITCH.ON) {
    throw new TypeError('the interrupt switch is INTERRUPT_SWITCH.OFF or .ON');
  }
  if (!Number.isInteger(queueCap) || queueCap < 1) throw new TypeError('queueCap is a positive whole number');
  if (dialogue !== null && typeof dialogue !== 'function') {
    throw new TypeError('utterance is the voice dialogue (P6.12), or absent');
  }

  /** D127's setting, in memory and nowhere else: it is never written down. */
  let switchValue = setting;
  /** P1.4's last answer, or `null` before one has arrived. */
  let presence = null;
  /** The digest's most severe severity, or `null` before a digest arrives. */
  let highest = null;
  /** What the OS answered about the microphone (D122), until it is asked. */
  let microphone = Object.freeze({ ok: true });
  /** The proactive updates waiting for a boundary, oldest first. */
  const queue = [];
  /**
   * The last refusal this screen produced, so a screen can show it, and `null`
   * once there is nothing to show. It is **cleared by the message that is
   * accepted after it**: a snapshot that went on saying `offline` over a message
   * that had just left the phone would be showing a state the phone is not in,
   * which is the one thing a status line must not do.
   */
  let lastRefused = null;
  /** The last thing heard, partial or final. */
  let transcript = null;
  /** P1.2's lease state, and the last time a lease call refused. */
  let leaseState = null;
  let leaseRefused = null;

  /** P1.4 says the orchestrator is not there. Only `offline` is `offline`. */
  const offline = () => presence !== null && presence.state === STATE.OFFLINE;

  /** Record a refusal the caller did not make, so the screen can show it. */
  function noteRefusal(refusal) {
    lastRefused = Object.freeze({ code: refusal.code, field: refusal.field ?? null });
    return refusal;
  }

  /**
   * Speak one queued update, if the rule allows one now.
   *
   * The rule is `interruptDecision`'s and nothing else: this function finds the
   * **first** update the decision says may interrupt and hands it to the
   * pipeline. Two consequences worth naming, because they are the plan's
   * sentences:
   *
   *   * the switch being off means nothing is ever spliced out, so an update is
   *     **queued and not spoken**;
   *   * the decision is asked with the pipeline's own `speaking()` and `turn()`,
   *     so an update is never spoken over an in-progress utterance or while the
   *     user is being listened to -- and the update stays queued rather than
   *     being lost to a refusal.
   *
   * One update per pass: the next pass is the pipeline's `utterance` event,
   * which is the next boundary.
   */
  function drain() {
    if (highest === null) return;
    for (let i = 0; i < queue.length; i++) {
      const verdict = interruptDecision({
        switch: switchValue,
        severity: queue[i].severity,
        highest,
        speaking: pipeline.speaking(),
        turn: pipeline.turn(),
      });
      if (!verdict.interrupt) continue;
      const [item] = queue.splice(i, 1);
      const said = pipeline.say(item.text);
      // A `say` that never started (an utterance that raced this one) is shown
      // rather than swallowed; the update is gone from the queue, and the owner
      // is the one who can tell whether that mattered.
      said.then((outcome) => {
        if (!outcome.ok) noteRefusal(outcome);
      });
      return;
    }
  }

  // --- the pipeline's events, which are what a boundary is ----------------

  // A turn's final: the user's words become a message the app accepts (or
  // refuses, while offline), and the turn closing is a boundary, so the queue
  // gets its chance.
  pipeline.on('message', (event) => {
    // P6.12's seam. A final transcript is not always a message: P6.1 routes it,
    // and a "Fugaro, confirm" is a confirmation rather than something to send.
    // The spoken dialogue is that routing, and the app passes it here; the
    // words it decides are agent text go to this screen's own `accept`, so the
    // offline refusal above stands over the voice path too. Without a dialogue
    // the screen is what P3.5 shipped: every final is a message, and `accept`
    // is the only door.
    if (dialogue === null) void accept(event.message.text);
    else void dialogue(event.message.text);
    drain();
  });
  // The user's words, as they arrive: what §5's screen shows.
  pipeline.on('transcript', (event) => {
    transcript = Object.freeze({ text: event.text, final: event.final });
  });
  // D122: the OS refused the microphone or the background audio mode.
  pipeline.on('unavailable', (event) => {
    microphone = Object.freeze({ ok: false, code: event.code });
  });
  // An utterance ended, either way: `finished` frees the boundary, and an
  // `interrupted` one leaves a turn open, which `drain` sees for itself.
  pipeline.on('utterance', () => drain());

  /**
   * Accept one message from the owner -- their own words, whatever produced
   * them -- and hand it to the client.
   *
   * **The offline refusal is here and nowhere else.** The spec's sentence is
   * that the app refuses *rather than accepting messages into the void*, and
   * this is the one door every message goes through (the pipeline's `message`
   * event and a typed one alike), so one guard is enough. The refusal is
   * recorded as well as returned: a refusal nobody renders is a message the
   * owner believes they sent.
   *
   * And a message that **is** accepted clears what the last refusal left: the
   * snapshot's `refused` is the screen's current state, not a history of every
   * refusal it ever made.
   *
   * @param {unknown} text what the owner said
   * @returns {Promise<object>} the client's own verdict, or this screen's refusal
   */
  async function accept(text) {
    if (typeof text !== 'string' || text.length === 0) return noteRefusal(refuse(SCREEN_CODES.BAD_TEXT, 'text'));
    if (offline()) return noteRefusal(refuse(SCREEN_CODES.OFFLINE, 'presence'));
    const sent = await client.send(text);
    if (sent.ok) lastRefused = null;
    return sent;
  }

  /**
   * Adopt P1.4's answer, whether a caller hands it over or `poll` brings it.
   *
   * One guard for both paths on purpose: `poll` is the path the app actually
   * takes, so a check that only a caller's `setPresence` went through would be
   * a rule enforced where it is not used. A state outside P1.4's own four is
   * refused rather than stored -- a screen showing a state nothing can produce
   * is worse than one showing the last real answer -- and the refusal is
   * recorded, because the caller of `poll` is not the one who chose it.
   *
   * @param {unknown} value P1.4's presence object
   */
  function setPresence(value) {
    if (value === null || typeof value !== 'object' || Array.isArray(value)) {
      return refuse(SCREEN_CODES.BAD_PRESENCE, null);
    }
    if (!PRESENCE_STATES.includes(value.state)) return refuse(SCREEN_CODES.BAD_PRESENCE, 'state');
    presence = value;
    drain();
    return Object.freeze({ ok: true });
  }

  return {
    /** Accept a message, from the pipeline's `message` event or a text screen. */
    accept,

    /**
     * D127's setting, as a value the owner changes. Never written down: the
     * switch is the phone's, and no other installation gets a vote on it.
     *
     * @param {unknown} value `INTERRUPT_SWITCH.OFF` or `.ON`
     */
    setSwitch(value) {
      if (value !== INTERRUPT_SWITCH.OFF && value !== INTERRUPT_SWITCH.ON) {
        return refuse(SCREEN_CODES.BAD_SWITCH, 'switch');
      }
      switchValue = value;
      drain();
      return Object.freeze({ ok: true, switch: switchValue });
    },

    /** Adopt P1.4's answer, as a caller hands it over. See the function above. */
    setPresence,

    /**
     * Adopt a digest, and take its most severe severity as the top of the
     * ladder. This is the only thing this screen reads out of P1.3's digest:
     * the items are the feed's business and the spoken sentence is P6.12's.
     *
     * @param {unknown} value a digest (`{ severity, … }`)
     */
    setDigest(value) {
      if (value === null || typeof value !== 'object' || Array.isArray(value)) {
        return refuse(SCREEN_CODES.BAD_DIGEST, null);
      }
      if (!SEVERITY_ORDER.includes(value.severity)) return refuse(SCREEN_CODES.BAD_SEVERITY, 'severity');
      highest = value.severity;
      drain();
      return Object.freeze({ ok: true, highest });
    },

    /**
     * Offer one proactive update. It is queued; whether it is spoken is
     * `interruptDecision`'s answer at the next boundary.
     *
     * @param {unknown} update `{ text, severity }`
     */
    offer(update) {
      if (update === null || typeof update !== 'object' || Array.isArray(update)) {
        return refuse(SCREEN_CODES.BAD_UPDATE, null);
      }
      for (const key of Object.keys(update)) {
        if (!UPDATE_FIELDS.includes(key)) return refuse(SCREEN_CODES.UNKNOWN_FIELD, key);
      }
      if (typeof update.text !== 'string' || update.text.length === 0) {
        return refuse(SCREEN_CODES.BAD_TEXT, 'text');
      }
      if (!SEVERITY_ORDER.includes(update.severity)) return refuse(SCREEN_CODES.BAD_SEVERITY, 'severity');
      // Nothing is queued against a digest that has not arrived: the ladder's
      // top is what decides whether an update may interrupt, and a queue with
      // no top is a queue nothing can drain.
      if (highest === null) return refuse(SCREEN_CODES.NO_DIGEST, 'digest');
      if (queue.length >= queueCap) return refuse(SCREEN_CODES.QUEUE_FULL, 'queue');
      queue.push(Object.freeze({ text: update.text, severity: update.severity }));
      drain();
      return Object.freeze({ ok: true, queued: queue.length });
    },

    /** What is waiting to be spoken, oldest first. */
    queued() {
      return freezeList(queue);
    },

    /**
     * One mailbox pass: the client's own `poll`, and the presence it reports is
     * what this screen shows and refuses on.
     *
     * The presence goes through `setPresence` rather than being stored here.
     * The client reports one of P1.4's own four states today, so the guard is
     * not what the app meets -- and that is exactly why it is on this path:
     * the rule is "a state outside P1.4's four is refused rather than stored",
     * and a rule checked only on a path the app does not take is a rule this
     * screen does not have. The client's report is returned as itself either
     * way; what the screen shows is what it adopted.
     */
    async poll() {
      const report = await client.poll();
      if (report.ok && report.presence !== undefined) {
        const adopted = setPresence(report.presence);
        // The caller did not choose this presence -- the mailbox did -- so a
        // refusal is shown rather than only returned. `setPresence` has already
        // drained if it adopted anything, and a second drain is a no-op: the
        // first one either spoke (so `speaking()` is true) or found nothing
        // eligible, which is what the second one finds too.
        if (!adopted.ok) noteRefusal(adopted);
      }
      drain();
      return report;
    },

    /** P1.4's last answer, or `null` before one has arrived. */
    presence() {
      return presence;
    },

    /**
     * Adopt a lease state read out of the database (P1.2's container: a claim
     * and a TTL). A state P1.2 will not read is refused, and the state this
     * screen already had is kept.
     *
     * @param {unknown} state P1.2's lease state
     */
    setLease(state) {
      const read = readLease(state);
      if (!read.ok) return refuse(SCREEN_CODES.BAD_LEASE, 'lease');
      leaseState = state;
      leaseRefused = null;
      return Object.freeze({ ok: true });
    },

    /** The claim on the lease right now, or P1.2's own refusal. */
    lease() {
      if (leaseState === null) return Object.freeze({ ok: true, lease: null });
      return readLease(leaseState);
    },

    /**
     * Ask for the lease, as this installation. P1.2's verdict is returned
     * **verbatim**, `already-claimed` and its `existing` claim included: a
     * lease another consumer holds is surfaced here rather than ignored, and
     * the state P1.2 hands back is adopted whether or not the take succeeded,
     * so what the screen shows is what the database says.
     */
    takeLease() {
      const taken = take(leaseState ?? emptyLeaseState(), { holder, at: now() });
      leaseState = taken.state;
      leaseRefused = taken.ok ? null : Object.freeze({ code: taken.code, holder: taken.existing?.holder ?? null });
      return taken;
    },

    /** Give the lease back, if it is this installation's own. */
    releaseLease() {
      if (leaseState === null) return refuse(SCREEN_CODES.BAD_LEASE, 'lease');
      const released = release(leaseState, { holder });
      leaseState = released.state;
      leaseRefused = released.ok ? null : Object.freeze({ code: released.code, holder: null });
      return released;
    },

    /** Ask the OS for the microphone (D122), and show what it answered. */
    async start() {
      const started = await pipeline.start();
      if (started.ok) microphone = Object.freeze({ ok: true });
      return started;
    },

    /** Stop listening. What the OS answered about the microphone is kept. */
    stop() {
      return pipeline.stop();
    },

    /**
     * Everything a screen renders, in one frozen snapshot. It reads the
     * pipeline for what is happening now (`speaking`, `turn`) rather than
     * caching it, so the snapshot cannot say something the pipeline would not.
     */
    state() {
      const claim = leaseState === null ? null : readLease(leaseState);
      return Object.freeze({
        switch: switchValue,
        presence,
        offline: offline(),
        microphone,
        queued: freezeList(queue),
        lease: claim !== null && claim.ok ? claim.lease : null,
        leaseRefused,
        speaking: pipeline.speaking(),
        turn: pipeline.turn(),
        transcript,
        refused: lastRefused,
      });
    },
  };
}
