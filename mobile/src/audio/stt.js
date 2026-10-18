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
// **Text crosses this port. Audio never does.** The event's fields are exactly
// `kind` and `text` (`STT_EVENT_FIELDS`, asserted by this module's tests), for
// D118's reason: on-device STT (D44) means no sample needs to leave the
// device, and a port that could carry one is a port a later change could start
// carrying one through.
//
// **Cancellation is the turn's, not the audio's.** `cancel()` abandons the
// open turn; the contract is that a cancelled turn produces **no** `final`.
// That matters more than it looks: §5's grace period lets the user say "cancel
// that" before the turn is sent, and a `final` arriving after the turn was
// abandoned would be a message the user asked not to send. The fake can
// deliver such a final anyway (`lateFinal`) -- an engine's flush racing the
// turn's end is exactly what a real one does -- and the pipeline must ignore
// it. That is a test, not a comment.

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

/** The exact fields a transcript event carries: the words, and nothing heard. */
export const STT_EVENT_FIELDS = Object.freeze(['kind', 'text']);

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
 * Its scripting surface is `partial(text)`, `final(text)` and `lateFinal(text)`
 * -- and the third is the one worth explaining. A fake that only ever did what
 * the contract promises would make the pipeline's protection against a late
 * `final` untestable: the fake would never produce the case. So the fake
 * **can** produce it, on purpose, with the word `late` in the call, and
 * `pipeline.js` has a test asserting that a message is not made from one.
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
  #handlers = new Map(STT_EVENTS.map((kind) => [kind, new Set()]));
  #emitted = [];
  #turns = 0;
  #cancels = 0;

  /** Open a turn. A turn is already open only while the engine holds one. */
  begin() {
    if (this.#state !== 'idle') return refuse(STT_CODES.ALREADY_LISTENING, 'turn');
    this.#state = 'open';
    this.#turns++;
    return accept({ changed: true });
  }

  /**
   * The user stopped speaking: flush. The engine's `final` follows (scripted,
   * in the fake) and **that** is what ends the turn -- which is why `end()`
   * does not close it. A second `end()` is a no-op: the turn is already
   * flushing, and a capture path that reports the end of speech twice is not
   * an error.
   */
  end() {
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
    this.#state = 'idle';
    this.#cancels++;
    return accept({ changed: true });
  }

  /** Whether the engine holds a turn -- open, or flushing its final. */
  listening() {
    return this.#state !== 'idle';
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
    return this.#emit('partial', text);
  }

  /** The engine's final transcript for the turn, which the final ends. */
  final(text) {
    if (this.#state === 'idle') return refuse(STT_CODES.NOT_LISTENING, 'turn');
    this.#state = 'idle';
    return this.#emit('final', text);
  }

  /**
   * The engine's `final` arriving with **no turn open**: the flush that raced a
   * cancel or a stop. The port never promises one; a real engine can deliver
   * one, and the pipeline must not make a message out of it.
   */
  lateFinal(text) {
    return this.#emit('final', text);
  }

  /** Every event this fake has delivered, in order, as frozen copies. */
  emitted() {
    return Object.freeze([...this.#emitted]);
  }

  /** How many turns were opened, and how many were cancelled. */
  turns() {
    return Object.freeze({ opened: this.#turns, cancelled: this.#cancels });
  }

  #emit(kind, text) {
    if (typeof text !== 'string') throw new TypeError('a transcript is a string');
    const event = Object.freeze({ kind, text });
    this.#emitted.push(event);
    for (const handler of [...this.#handlers.get(kind)]) handler(event);
    return accept({ event });
  }
}
