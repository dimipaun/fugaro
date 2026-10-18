// The speech output port: streaming text-to-speech, cancellable mid-utterance
// (mobile plan, Task P3.4; D122, D123, D127).
//
// **The unit is the utterance, and its outcome is a value.** `speak(text)`
// hands one utterance to the output and answers with what became of it:
// `{ ok: true, spoken: true }` when it played to the end, or `interrupted`
// when it did not. That is the whole interface, and it is deliberately not an
// event stream: the plan's verification names one property -- *an interrupted
// utterance is reported as interrupted* -- and a promise that resolves with
// the outcome is one place for that fact rather than two.
//
// **Why cancellation is a method and not an afterthought.** §5's barge-in is
// "user speech during TTS stops playback immediately"; the acceptance
// criterion is that it happens within ~300 ms. The mechanism is that the
// caller can cancel what is playing, and `pipeline.js` is the caller: a
// `speech-start` or a `barge-in` arriving while an utterance is in flight
// cancels it, and the in-flight `speak` resolves `interrupted`. D52's answer
// puts exactly this in CI -- the cancellation is a property of the pipeline
// against a fake player, and the ~300 ms is the owner-run half (check 86).
//
// **One utterance at a time.** A second `speak` while one is playing is
// refused with `already-speaking` rather than queued: queueing proactive
// updates behind one another is the prompt queue's job (D127's interrupt
// switch, P6.5), and it is a policy, not an output. An output that queued
// would make "never speak over an in-progress utterance" a property of the
// engine rather than a decision the app can show.
//
// **D123 again: there is no engine here.** The local-only answer (D44) and the
// bake-off (check 85) are what decide which engine speaks; this port is what
// P3.5's screen and P6.12's read-back are written against in the meantime.
//
// **What the app never does with an utterance.** It is not recorded, spooled
// or stored (D118: the app never stores or uploads audio, and the mailbox
// carries text). The port takes text and reports an outcome; there is no call
// here that hands back audio, and `index.js`'s test asserts the event and
// return shapes.

import { accept, refuse } from './refusal.js';

/** The refusal codes this port returns. */
export const TTS_CODES = Object.freeze({
  /** The text is not a non-empty string: there is nothing to say. */
  BAD_TEXT: 'bad-text',
  /** One utterance at a time; a second one is a policy decision, not a queue. */
  ALREADY_SPEAKING: 'already-speaking',
  /** The utterance did not play to the end (barge-in, or a stopped session). */
  INTERRUPTED: 'interrupted',
  /** `finish()` with nothing in flight: the fake's own scripting refusal. */
  NOT_SPEAKING: 'not-speaking',
});

/**
 * Whether `value` is a speech output port: the three methods, as functions.
 *
 * @param {unknown} value a candidate port
 * @returns {boolean}
 */
export function isTextToSpeech(value) {
  if (value === null || typeof value !== 'object') return false;
  return ['speak', 'cancel', 'speaking'].every((name) => typeof value[name] === 'function');
}

/**
 * The fake speech output: nothing is synthesized, and **the test decides when
 * the utterance ends** (D52, D126).
 *
 * There is no timer in it. `speak(text)` returns a promise that settles only
 * when the test (or the pipeline) calls `finish()` or `cancel()` -- so a
 * barge-in can be asserted at the exact moment it is decided, and a test that
 * forgets to end an utterance hangs rather than passing by accident.
 *
 * @example
 *   const tts = new FakeTts();
 *   const said = tts.speak('done');      // in flight
 *   tts.cancel();                        // barge-in
 *   await said;                          // { ok: false, code: 'interrupted' }
 */
export class FakeTts {
  #flight = null;
  #spoken = [];
  #interrupted = [];
  #cancels = 0;

  /**
   * Speak one utterance. Answers `{ ok: true, spoken: true }` when it finishes,
   * a `bad-text` or `already-speaking` refusal when it never starts, and an
   * `interrupted` refusal when it is cancelled part-way.
   *
   * @param {string} text what to say
   * @returns {Promise<{ ok: true, spoken: true } | { ok: false, code: string, field: string | null }>}
   */
  async speak(text) {
    if (typeof text !== 'string' || text.length === 0) return refuse(TTS_CODES.BAD_TEXT, 'text');
    if (this.#flight !== null) return refuse(TTS_CODES.ALREADY_SPEAKING, 'text');
    this.#spoken.push(text);
    // The test decides when this settles. The executor runs synchronously, so
    // `#flight` is set before `speak` has returned -- a caller may cancel on
    // the very next line, which is exactly what a barge-in test does.
    return new Promise((resolve) => {
      this.#flight = { text, settle: resolve };
    });
  }

  /**
   * Stop what is playing. The in-flight `speak` resolves `interrupted`, which
   * is the reporting the plan asks for. A cancel with nothing playing is a
   * no-op, not an error: a session that stops twice is not a failure.
   */
  cancel() {
    if (this.#flight === null) return accept({ changed: false });
    const flight = this.#flight;
    this.#flight = null;
    this.#cancels++;
    this.#interrupted.push(flight.text);
    flight.settle(refuse(TTS_CODES.INTERRUPTED, 'text'));
    return accept({ changed: true });
  }

  /** Whether an utterance is in flight right now. */
  speaking() {
    return this.#flight !== null;
  }

  // ---- the scripting surface: what only a fake has ----------------------

  /** Let the in-flight utterance end, as playback finishing does. */
  finish() {
    if (this.#flight === null) return refuse(TTS_CODES.NOT_SPEAKING, 'utterance');
    const flight = this.#flight;
    this.#flight = null;
    flight.settle(accept({ spoken: true }));
    return accept({ changed: true });
  }

  /** Every text handed to this output, in order. */
  spoken() {
    return Object.freeze([...this.#spoken]);
  }

  /** Every text that was cancelled mid-utterance, in order. */
  interrupted() {
    return Object.freeze([...this.#interrupted]);
  }

  /** How many cancels actually stopped something. */
  cancels() {
    return this.#cancels;
  }
}
