// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The transcription port: streaming speech-to-text (mobile plan, Task P3.4;
// D123, D118, D126).
//
// **One turn at a time, and the turn is the unit.** `begin()` opens a turn
// when the capture path says the user started speaking, `end()` puts it into
// its **flush** when the capture path says the user stopped -- the engine
// answers with `partial` events while it listens and one `final` event per
// turn, and the `final` is what ends the turn. The distinction is not
// pedantry: a final arrives *after* the end of speech, so an `end()` that
// closed the turn would leave the engine's answer with nowhere to land. The
// pipeline (`pipeline.js`) is what drives those calls from `AudioIn`'s
// events; this port knows nothing about the microphone, which is what makes it
// fakeable without a device.
//
// **Partials are not optional.** D123's whole point is a measurement: the
// bake-off's stated latency target is *first partial transcript* and
// *end-of-speech to spoken-reply*, measured on the oldest supported device, so
// the first partial is a fact the interface has to carry rather than a detail
// of one engine. A partial is also what §5's on-screen transcript shows while
// the user is still speaking -- and it is **not** a message: only a `final`
// becomes one, and `pipeline.js` is where that rule lives.
//
// **Every event names the turn it belongs to.** `begin()` answers with a
// `turnId`, and every `partial` and `final` carries it back
// (`STT_EVENT_FIELDS`). This is not decoration: an engine's flush can outlive
// the turn that asked for it, and **the words alone cannot say which turn they
// are the words of.** A pipeline that keyed only on "is a turn open" would take
// an abandoned turn's final for the current turn's -- sending the text the user
// cancelled, and swallowing the text they actually said, which arrives a moment
// later under the *same* open-turn state. The id is what makes "ignore a turn I
// am no longer in" a rule the port can hold, rather than a race the pipeline
// hopes not to lose. An engine that cannot echo it cannot implement this port,
// and one that answers a final for a turn it no longer holds is out of step --
// so the port says whose words they are, and `pipeline.js` compares.
//
// **Text crosses this port. Audio never does.** The event's fields are exactly
// `kind`, `turnId` and `text`, for D118's reason: on-device STT (D44) means no
// sample needs to leave the device, and a port that could carry one is a port a
// later change could start carrying one through. A turn id is an identity, not
// a signal -- it says nothing about what was heard.
//
// **Cancellation is the turn's, not the audio's.** `cancel()` abandons the
// open turn; the contract is that a cancelled turn produces **no** `final`.
// That matters more than it looks: §5's grace period lets the user say "cancel
// that" before the turn is sent, and a `final` arriving after the turn was
// abandoned would be a message the user asked not to send. The fake can
// deliver such a final anyway (`lateFinal`, carrying the abandoned turn's id)
// -- an engine's flush racing the turn's end is exactly what a real one does --
// and the pipeline must ignore it. That is a test, not a comment.

import { accept, refuse } from './refusal.js';

/** The refusal codes this port returns. */
export const STT_CODES = Object.freeze({
  /** `begin()` with a turn already open: one turn at a time. */
  ALREADY_LISTENING: 'already-listening',
  /** `end()` or a scripted partial with no turn open. */
  NOT_LISTENING: 'not-listening',
});

/** The events a transcription port may emit. */
export const STT_EVENTS = Object.freeze(['partial', 'final']);

/** The exact fields a transcript event carries: which turn, the words, and
 * nothing heard. */
export const STT_EVENT_FIELDS = Object.freeze(['kind', 'turnId', 'text']);

/**
 * Whether `value` is a transcription port: the five methods, as functions.
 *
 * @param {unknown} value a candidate port
 * @returns {boolean}
 */
export function isSpeechToText(value) {
  if (value === null || typeof value !== 'object') return false;
  return ['begin', 'end', 'cancel', 'listening', 'on'].every((name) => typeof value[name] === 'function');
}

/**
 * The fake transcription port: no engine, no audio, every transcript scripted
 * (D52, D126).
 *
 * Its scripting surface is `partial`, `final`, and the two escapes `lateFinal`
 * and `latePartial`, which carry the **abandoned** turn's id. Those two are the
 * ones worth explaining. A fake that only ever did what the contract promises
 * would make the pipeline's protection against a late answer untestable: the
 * fake would never produce the case. So the fake **can** produce it, on purpose,
 * with the word `late` in the call, and `pipeline.js` has tests asserting that
 * neither a message nor a transcript comes of one.
 *
 * The one thing the fake cannot do is be *accidentally* out of step: `partial`
 * and `final` refuse when no turn is the engine's, and every event they emit
 * carries the open turn's id. `outOfStep` is the deliberate exception, and it
 * exists for the same reason the two escapes do -- it is the only way to reach
 * the pipeline's defensive branches, so without it those branches would be code
 * no test could hold to anything.
 *
 * @example
 *   const stt = new FakeStt();
 *   stt.on('final', (e) => said.push(e.text));
 *   stt.begin();
 *   stt.partial('hel');
 *   stt.final('hello');           // said === ['hello']
 */
export class FakeStt {
  /** `idle`, `open` (the user is speaking) or `flushing` (speech ended, the
   * engine is producing its final). */
  #state = 'idle';
  /** The turn `begin()` opened, and the identity every event carries back.
   * Zero means no turn: it is never a turn's id, so nothing a test can emit
   * accidentally matches one. */
  #turnId = 0;
  /** The id the next `begin()` takes, so ids are never reused within a fake. */
  #nextTurnId = 1;
  /** The last turn a `cancel()` abandoned, so `lateFinal`/`latePartial` can
   * name the flush that outlived it. */
  #abandonedTurnId = 0;
  #outOfStep;
  #handlers = new Map(STT_EVENTS.map((kind) => [kind, new Set()]));
  #emitted = [];
  #turns = 0;
  #cancels = 0;

  /**
   * @param {object} [options]
   * @param {'begin' | 'end' | null} [options.outOfStep] make the engine answer
   *   one call as though it were in the other state -- `begin()` saying it is
   *   already listening, or `end()` saying it is not listening. An engine that
   *   has lost the turn does exactly this, and it is the only way to reach
   *   `pipeline.js`'s two defensive branches.
   */
  constructor({ outOfStep = null } = {}) {
    if (outOfStep !== null && outOfStep !== 'begin' && outOfStep !== 'end') {
      throw new TypeError("outOfStep must be 'begin', 'end' or null");
    }
    this.#outOfStep = outOfStep;
  }

  /**
   * Open a turn, and answer with its id. That id is what every event of the
   * turn carries, so a caller can tell one turn's words from another's.
   */
  begin() {
    if (this.#outOfStep === 'begin') return refuse(STT_CODES.ALREADY_LISTENING, 'turn');
    if (this.#state !== 'idle') return refuse(STT_CODES.ALREADY_LISTENING, 'turn');
    this.#state = 'open';
    this.#turnId = this.#nextTurnId++;
    this.#turns++;
    return accept({ changed: true, turnId: this.#turnId });
  }

  /**
   * The user stopped speaking: flush. The engine's `final` follows (scripted,
   * in the fake) and **that** is what ends the turn -- which is why `end()`
   * does not close it. A second `end()` is a no-op: the turn is already
   * flushing, and a capture path that reports the end of speech twice is not
   * an error.
   */
  end() {
    if (this.#outOfStep === 'end') return refuse(STT_CODES.NOT_LISTENING, 'turn');
    if (this.#state === 'idle') return refuse(STT_CODES.NOT_LISTENING, 'turn');
    if (this.#state === 'flushing') return accept({ changed: false });
    this.#state = 'flushing';
    return accept({ changed: true });
  }

  /**
   * Abandon the turn, open or flushing: no `final` follows it. A cancel with
   * no turn is a no-op rather than a refusal, because a screen that cancels
   * twice is not an error.
   */
  cancel() {
    if (this.#state === 'idle') return accept({ changed: false });
    this.#abandonedTurnId = this.#turnId;
    this.#turnId = 0;
    this.#state = 'idle';
    this.#cancels++;
    return accept({ changed: true });
  }

  /** Whether the engine holds a turn -- open, or flushing its final. */
  listening() {
    return this.#state !== 'idle';
  }

  /**
   * The open turn's id, or **0 when the engine holds no turn**. A test needs it
   * to script the hostile cases below: `latePartial(text, stt.turnId())` is a
   * revision the engine sends for the turn it was just told to stop.
   */
  turnId() {
    return this.#turnId;
  }

  /**
   * Subscribe to one event kind. Returns an idempotent unsubscribe; a bad kind
   * or a handler that is not a function throws (see `refusal.js`).
   */
  on(kind, handler) {
    if (!STT_EVENTS.includes(kind)) throw new TypeError(`unknown event: ${kind}`);
    if (typeof handler !== 'function') throw new TypeError('a handler is a function');
    this.#handlers.get(kind).add(handler);
    return () => this.#handlers.get(kind).delete(handler);
  }

  // ---- the scripting surface: what only a fake has ----------------------

  /** The engine heard this much, so far. Refused once the turn is flushing:
   * after `end()` there is nothing more to revise. */
  partial(text) {
    if (this.#state !== 'open') return refuse(STT_CODES.NOT_LISTENING, 'turn');
    return this.#emit('partial', text, this.#turnId);
  }

  /** The engine's final transcript for the turn, which the final ends. */
  final(text) {
    if (this.#state === 'idle') return refuse(STT_CODES.NOT_LISTENING, 'turn');
    const turnId = this.#turnId;
    this.#state = 'idle';
    this.#turnId = 0;
    return this.#emit('final', text, turnId);
  }

  /**
   * The engine's `final` for a turn it no longer holds -- by default the one a
   * cancel abandoned: the flush that raced a cancel or a stop. The port never
   * promises one; a real engine can deliver one, and the pipeline must not make
   * a message out of it. Pass `turnId` to script a final for any turn, or none.
   *
   * @param {string} text the words
   * @param {number} [turnId] the turn they belong to (default: the abandoned one)
   */
  lateFinal(text, turnId = this.#abandonedTurnId) {
    return this.#emit('final', text, turnId);
  }

  /**
   * The engine's `partial` out of a turn it is not open in -- by default the
   * abandoned one, and with `stt.turnId()` the turn the capture path has just
   * ended. Either way the words are shown by nothing: a revision arrives while
   * the user is speaking, and not after.
   *
   * @param {string} text the words
   * @param {number} [turnId] the turn they belong to (default: the abandoned one)
   */
  latePartial(text, turnId = this.#abandonedTurnId) {
    return this.#emit('partial', text, turnId);
  }

  /** Every event this fake has delivered, in order, as frozen copies. */
  emitted() {
    return Object.freeze([...this.#emitted]);
  }

  /** How many turns were opened, and how many were cancelled. */
  turns() {
    return Object.freeze({ opened: this.#turns, cancelled: this.#cancels });
  }

  #emit(kind, text, turnId) {
    if (typeof text !== 'string') throw new TypeError('a transcript is a string');
    const event = Object.freeze({ kind, turnId, text });
    this.#emitted.push(event);
    for (const handler of [...this.#handlers.get(kind)]) handler(event);
    return accept({ event });
  }
}
