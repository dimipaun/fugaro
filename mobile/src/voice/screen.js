// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The voice screen: the interrupt switch, the queue it gates, the offline
// refusal and the lease (mobile plan, Task P3.5; D122, D127, D126, D118), and
// the indicator's place and the per-utterance engine tag (Task P5.3.7; the
// design is docs/mobile/design/voice-evaluation-switch.md §4.2 and §4.4).
//
// **What this module is.** P3.4 shipped the audio interfaces and their fakes;
// P3.6 shipped the mailbox client, the app's only writer; P1.2, P1.3 and P1.4
// shipped the lease, the severity ladder and the presence rule. This is the
// screen those five are wired into, and it owns exactly four decisions:
//
//   1. **the interrupt switch (D127)** -- a local phone setting, never written
//      to the database, gating whether a proactive update may be spoken now
//      (`interrupt.js` holds the rule; this file holds the queue it drains, and
//      the hold VS1.1's session puts on that queue while the session is paused
//      or over -- see `setHeld`, which is the one decision here that is the
//      session's rather than this file's);
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
// **What it deliberately does not do.** No spoken confirmation (P6.12, which
// builds on this task later: the seam is `accept` and the pipeline's events, and
// none of it is implemented here), no prompt queue (P6.5, which is not a
// dependency of this task -- see `interrupt.js` on the one place the two
// disagree), and no writing of its own: **every** write goes through the client
// (`accept` calls `client.send`, and asks `client.licenceRefusal` before its own
// offline refusal so the licence's answer wins -- a question, not a write),
// which is what keeps D113's "one write path" true.
//
// **And it chooses no engine** (D123/D46: on-device only, chosen by a bake-off
// -- owner-run check 85). What P5.3.7 adds here is the two places the switch and
// the egress path need a *screen*, and neither of them is a choice about where
// an utterance goes:
//
//   1. **The indicator has its place** (§4.2, and the plan's own sentence). The
//      screen holds the badge the voice switch answered, for each of the two
//      directions, and shows P5.3.6's fail-closed badge until the switch answers
//      at all -- §4.2's "until that first answer arrives the app shows
//      `local — checking`, never a guess". It draws nothing itself: the words,
//      the engine, the provider and `send` are P5.3.6's `renderIndicator`, and
//      the badge is not dismissible because that module says so. It is asked
//      once here and again whenever the app asks (`refreshIndicator`), which is
//      §4.4 point 7's own list -- the switch changing, the app coming to the
//      foreground, a pairing -- and never a timer this file keeps.
//   2. **Every utterance in the transcript is tagged with the engine that
//      produced it** (§4.2), so the evaluation has a per-utterance record and
//      not only a badge. The tag is read from a port at the moment the words
//      arrive -- an utterance's engine is a fact about that utterance, and
//      reading it later would tag the fifth line with the sixth route.
//
// Both are ports, and both are optional: a screen built without them is P3.5's
// screen, with no badge source to ask and the on-device engine as the only
// engine a transcript can have come from.
//
// **The line the owner speaks goes into the one conversation** (Task X1; D2788:
// *one transcript for voice and text*). This screen is the voice view, so a
// message it accepts is a message that was **said**: it is recorded as the
// operator's own line, carrying the engine tag read at that moment, and
// `src/text/transcript.js` holds it for the text view to draw. The recording is
// a fact of the accept rather than a second door: a message the client refused
// was never said to anybody and is not recorded (the text view's `unsent` is
// where refused text stays visible), and a line whose session has signed out is
// refused by the store rather than appended to a conversation that is over.
// With no transcript wired the screen behaves exactly as P3.5's did.
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
import { ENGINE } from '../../../shared/voice-switch/index.js';
import { isVoicePipeline } from '../audio/index.js';
import { TRANSCRIPT_SOURCE, TRANSCRIPT_WHO, isTranscript } from '../text/index.js';
import { DIRECTIONS, INDICATOR_FIELDS, failClosedIndicator } from '../voice-switch/index.js';
import { INTERRUPT_SWITCH, interruptDecision } from './interrupt.js';

/** The refusal codes this module answers with. */
export const SCREEN_CODES = Object.freeze({
  /** The switch is neither `off` nor `on`. */
  BAD_SWITCH: 'bad-switch',
  /** The session's hold on the queue is not a boolean. */
  BAD_HELD: 'bad-held',
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
  /**
   * No badge has arrived from the voice switch -- it has not been asked yet, or
   * the source threw. A badge is still owed (§4.2), so the fail-closed one
   * stands and this code is what says it is a stand-in rather than an answer.
   */
  NO_BADGE: 'no-badge',
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
 * The badge a direction shows when the switch has not answered, built by the
 * voice switch's own fail-closed function rather than drawn here: the words
 * (`local — checking`), the engine (`local`) and `send: false` are P5.3.6's, and
 * a second place that could spell them would be a second place they could
 * drift. What this file adds is the refusal in it, which says the badge is a
 * stand-in rather than an answer.
 */
function noBadge(direction) {
  return failClosedIndicator({ direction }, { code: SCREEN_CODES.NO_BADGE, field: 'indicator' });
}

/**
 * Whether a switch's answer is a badge this screen may render.
 *
 * The field set is `INDICATOR_FIELDS` exactly -- P5.3.6's own constant -- so a
 * badge carrying a field this screen does not know is refused here rather than
 * rendered as if the extra field were part of the badge. An answer that is not
 * a badge at all (a refusal, a string, `undefined`) is refused the same way.
 */
function isBadge(answer) {
  if (answer === null || typeof answer !== 'object' || Array.isArray(answer)) return false;
  const fields = Object.keys(answer);
  return fields.length === INDICATOR_FIELDS.length
    && INDICATOR_FIELDS.every((field) => Object.hasOwn(answer, field));
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
 * @param {(direction: string) => Promise<object>} [options.indicator] the voice
 *   switch's badge for one direction (P5.3.6's `createVoiceSwitch(...).indicator`);
 *   absent means the screen has no switch to ask and shows the fail-closed badge
 * @param {() => string} [options.engine] the engine that produced the utterance
 *   now arriving (P5.3.7's route: `local`, or the provider's name); absent means
 *   this device's own engine, which is the only one P3.5's pipeline has
 * @param {object} [options.transcript] the one conversation voice and text
 *   share (X1's `createTranscript`); absent means nothing is recorded and a
 *   snapshot's `conversation` is empty
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
  indicator: badgeSource = null,
  engine: engineOf = null,
  transcript = null,
} = {}) {
  if (!isVoicePipeline(pipeline)) throw new TypeError('pipeline is a VoicePipeline');
  // `licenceRefusal` is the third name this screen needs (S6.3): the offline
  // refusal asks the licence first, and a client that cannot be asked would
  // turn that into a crash inside `accept` rather than a wiring failure here.
  if (client === null || typeof client !== 'object'
    || typeof client.send !== 'function' || typeof client.poll !== 'function'
    || typeof client.licenceRefusal !== 'function') {
    throw new TypeError('client is a mailbox client: send(text), poll() and licenceRefusal(text)');
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
  if (badgeSource !== null && typeof badgeSource !== 'function') {
    throw new TypeError('indicator is the voice switch\'s badge source, or absent');
  }
  if (engineOf !== null && typeof engineOf !== 'function') {
    throw new TypeError('engine is a function answering the last utterance\'s engine, or absent');
  }
  if (transcript !== null && !isTranscript(transcript)) {
    throw new TypeError('transcript is X1\'s createTranscript value, or absent');
  }

  /** D127's setting, in memory and nowhere else: it is never written down. */
  let switchValue = setting;
  /**
   * Whether VS1.1's session is holding the queue, and `false` until it says so:
   * a screen with no session over it is the one P3.5 shipped with, and it
   * speaks. The two facts behind it -- the session is paused, the session is
   * over -- are the session's and not this file's, and neither is one of the
   * four `interruptDecision` is asked (see `setHeld`).
   */
  let held = false;
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
  /**
   * The last thing heard, partial or final -- P3.5's own slot, and not the one
   * transcript of X1 below: that one is the `transcript` this screen was handed
   * and holds the whole conversation, while this is what the microphone is
   * hearing right now. The snapshot keeps the name `transcript` for it, because
   * that is the field a view already renders.
   */
  let lastHeard = null;
  /** P1.2's lease state, and the last time a lease call refused. */
  let leaseState = null;
  let leaseRefused = null;
  /**
   * The badge per direction, as the switch last answered it, and P5.3.6's
   * fail-closed badge before and instead of an answer: §4.2's "until that first
   * answer arrives the app shows `local — checking`, never a guess". Both slots
   * are filled before anything is asked, so a snapshot a screen renders always
   * has a badge in it -- a blank where a promise about the owner's speech
   * belongs is the one thing the indicator may not be.
   */
  const badges = {};
  for (const direction of DIRECTIONS) badges[direction] = noBadge(direction);

  /** P1.4 says the orchestrator is not there. Only `offline` is `offline`. */
  const offline = () => presence !== null && presence.state === STATE.OFFLINE;

  /** Record a refusal the caller did not make, so the screen can show it. */
  function noteRefusal(refusal) {
    lastRefused = Object.freeze({ code: refusal.code, field: refusal.field ?? null });
    return refusal;
  }

  /** The two badges, frozen, for the snapshot. */
  const frozenBadges = () => Object.freeze({ ...badges });

  /**
   * Ask the voice switch for each direction's badge, and keep what it answers.
   *
   * The two directions are asked **one after the other** and not with
   * `Promise.all`: a badge source that throws must not leave the second
   * direction's ask unanswered, and one `await` in a loop is a sentence a
   * reader can check. A source that refuses or throws leaves the fail-closed
   * badge standing, which is the same thing the screen shows before it has
   * asked at all -- §4.2's `local — checking`, never a guess -- and an answer
   * that is not a badge is refused the same way rather than rendered.
   *
   * **It is not a poll and it is not a timer.** §4.4 point 7 names when the app
   * asks: the switch changing, the app coming to the foreground, a pairing.
   * Every one of those is the app's own event, and this screen keeps no clock
   * over it.
   */
  async function refreshIndicator() {
    for (const direction of DIRECTIONS) {
      let answer = null;
      try {
        answer = badgeSource === null ? null : await badgeSource(direction);
      } catch {
        // Thrown values are not carried: a source that threw is no answer, and
        // what it threw may be anything the phone holds.
        answer = null;
      }
      badges[direction] = isBadge(answer) ? answer : noBadge(direction);
    }
    return frozenBadges();
  }

  /**
   * The engine that produced the utterance arriving now.
   *
   * With no port this device's own engine is the only one P3.5's pipeline has,
   * so the tag is `local` (D123/D46). A tag that is not a non-empty string -- a
   * source that threw, or answered something else -- is **no tag at all** and
   * not `local`: a line that said `local` about words that may have reached a
   * provider would be the one lie the badge exists to prevent, and the
   * transcript is the per-utterance half of that badge (§4.2).
   */
  function readEngineTag() {
    if (engineOf === null) return ENGINE.LOCAL;
    let tag = null;
    try {
      tag = engineOf();
    } catch {
      tag = null;
    }
    return typeof tag === 'string' && tag.length > 0 ? tag : null;
  }

  /**
   * Speak one queued update, if the rule allows one now.
   *
   * The rule is `interruptDecision`'s, over the queue the session may be
   * holding: this function finds the **first** update the decision says may
   * interrupt and hands it to the pipeline. Three consequences worth naming,
   * because they are the plan's sentences:
   *
   *   * the switch being off means nothing is ever spliced out, so an update is
   *     **queued and not spoken**;
   *   * the decision is asked with the pipeline's own `speaking()` and `turn()`,
   *     so an update is never spoken over an in-progress utterance or while the
   *     user is being listened to -- and the update stays queued rather than
   *     being lost to a refusal;
   *   * a held queue is not drained at all, however free the boundary is: a
   *     session that is paused or over says nothing out loud (see `setHeld`).
   *
   * One update per pass: the next pass is the pipeline's `utterance` event,
   * which is the next boundary.
   */
  function drain() {
    if (held || highest === null) return;
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
  // The user's words, as they arrive: what §5's screen shows. The engine is read
  // here, once, and not later: it is a fact about **this** utterance, and a
  // snapshot that read the route when it was rendered would tag the third line
  // with the fourth utterance's engine.
  pipeline.on('transcript', (event) => {
    lastHeard = Object.freeze({ text: event.text, final: event.final, engine: readEngineTag() });
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
   * **The licence is asked first, and its answer wins over this screen's**
   * (S6.3; the design's §5.7). Being offline and having lapsed can both be true
   * of one utterance, and only one of the two sentences helps: "the
   * orchestrator is offline" sends the owner to wait for a network that is not
   * the problem, while the licence's names the key screen, which is never
   * gated. A pre-check that answered first would be exactly the masking the
   * design forbids -- and this screen cannot make the licence's refusal itself
   * (the lapse is decided in the write path and nowhere else), so it asks the
   * client before it refuses anything of its own. Nothing is written either
   * way: the licence's refusal is reported by the client, and this screen's
   * refusal is not a write at all.
   *
   * And a message that **is** accepted clears what the last refusal left: the
   * snapshot's `refused` is the screen's current state, not a history of every
   * refusal it ever made.
   *
   * `options` is the classification the caller already made, handed on to the
   * client unchanged: P6.12's dialogue passes `{ verb: 'launch' }` for D162's
   * pass-through, because the client cannot tell a launch from chatter and the
   * lapse (S6.3) refuses the one and keeps the other. A caller with nothing to
   * classify passes nothing, and this screen classifies nothing itself.
   *
   * @param {unknown} text what the owner said
   * @param {{action?: object, verb?: string}} [options] what the caller knows
   *   about it: the client's own options, forwarded
   * @returns {Promise<object>} the client's own verdict, or this screen's refusal
   */
  async function accept(text, options) {
    if (typeof text !== 'string' || text.length === 0) return noteRefusal(refuse(SCREEN_CODES.BAD_TEXT, 'text'));
    // The engine that produced THIS utterance, read before anything is awaited:
    // the send can take a round trip, and a tag read after it is the tag of
    // whatever the owner said next -- the same lie the `transcript` event's own
    // comment forbids, one await later.
    const engine = readEngineTag();
    if (offline()) {
      const refusedByLicence = await client.licenceRefusal(text, options);
      // The licence's refusal is the client's and is returned as itself -- it
      // is not recorded here, because this screen records its own refusals and
      // this one is not this screen's (the rule the client's stale refusal
      // follows too).
      if (refusedByLicence !== null) return refusedByLicence;
      return noteRefusal(refuse(SCREEN_CODES.OFFLINE, 'presence'));
    }
    const sent = await client.send(text, options);
    if (!sent.ok) return sent;
    lastRefused = null;
    // The words are the owner's own and they have reached the session: this is
    // the voice view's half of the one conversation (X1, D2788), recorded here
    // because the engine tag is a fact about this utterance and only this
    // screen holds it. The store's own verdict is not this door's answer to
    // give: the message has already left the phone, and what is left is whether
    // the line was kept -- one that was not (a session that signed out
    // mid-sentence) is a conversation this screen must not pretend is live.
    record(text, engine);
    return sent;
  }

  /**
   * Add one line to the one conversation, if a transcript is wired. The verdict
   * is deliberately dropped: see `accept`. `engine` is the tag `accept` read
   * when the utterance arrived -- carrying it in rather than reading it here is
   * the whole point: by the time the send has answered, the pipeline may have
   * heard the next utterance, and a line tagged with its engine is a line the
   * conversation lies about.
   */
  function record(text, engine) {
    if (transcript === null) return;
    transcript.append({
      who: TRANSCRIPT_WHO.OPERATOR,
      source: TRANSCRIPT_SOURCE.VOICE,
      text,
      engine,
    });
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

  // The badge is in place before anything is spoken (§4.2), so the screen asks
  // the switch once as it is built. Nothing waits on this one: the badge it
  // replaces is the fail-closed one, which is already renderable -- and an app
  // that wants to render *after* the switch has answered awaits
  // `refreshIndicator` itself, off the returned screen.
  void refreshIndicator();

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

    /**
     * Hold the queue, or let it go.
     *
     * This is VS1.1's session speaking, and the only thing that knows the two
     * facts `interruptDecision` is never asked: that the session is paused (§4's
     * paused states are "mic closed, playback paused", and §6.3's unsafe route
     * "must not continue speaking through the loudspeaker"), and that it is
     * over. The decision's four arguments are about the update and the phone --
     * the switch, the ladder's top, an utterance in flight, the user's turn --
     * and none of them is either fact.
     *
     * What makes the hold necessary is that both states are reached **by**
     * `stop()`: the pause's and the teardown's. A stop cancels the utterance in
     * flight, the cancel reports itself as an `utterance` event, and that event
     * is wired to `drain` like any other boundary -- so without the hold, the
     * stop that exists to silence the loudspeaker speaks the queue's next
     * update with it.
     *
     * Held means nothing is spoken and nothing is dropped: the queue keeps its
     * order and its contents, and letting go drains it again.
     *
     * @param {unknown} value `true` to hold the queue, `false` to let it go
     */
    setHeld(value) {
      if (typeof value !== 'boolean') return refuse(SCREEN_CODES.BAD_HELD, 'held');
      const was = held;
      held = value;
      // Letting go is the boundary, and it is drained only on the way out of a
      // hold: a session that re-states `false` on every state change -- VS1.1
      // does, since its rule is "paused or over" -- would otherwise hand the
      // queue a boundary it never asked for.
      if (was && !held) drain();
      return Object.freeze({ ok: true, held });
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
     * Ask the voice switch for both badges and keep the answers (§4.2, §4.4
     * point 7). The app calls this when the switch changes, when it comes to
     * the foreground and after a pairing; this screen asks once by itself, when
     * it is built, so the indicator is in place before anything is spoken.
     */
    refreshIndicator,

    /**
     * Everything a screen renders, in one frozen snapshot. It reads the
     * pipeline for what is happening now (`speaking`, `turn`) rather than
     * caching it, so the snapshot cannot say something the pipeline would not.
     *
     * `conversation` is the one transcript's lines, oldest first (X1, D2788):
     * the same list the text view draws, so the voice view shows what was said
     * while the owner was typing and the text view shows what was said while
     * they were speaking. Empty without a transcript.
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
        transcript: lastHeard,
        conversation: transcript === null ? Object.freeze([]) : transcript.entries(),
        indicator: frozenBadges(),
        refused: lastRefused,
      });
    },
  };
}
