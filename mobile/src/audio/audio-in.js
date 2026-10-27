// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The microphone's port: capture, voice activity detection, end-of-turn
// detection and the barge-in signal (mobile plan, Task P3.4; D122, D123, D118).
//
// **What an interface is, in this tree.** There is no TypeScript here and no
// `interface` keyword: a port is an object with methods, the module that
// consumes it checks its shape where it is constructed (`pipeline.js` does,
// as `AccountStore` does with its three stores), and `isAudioIn` is that
// check. The value of writing it down is that P3.5's screen and P6.12's
// confirmation dialogue are built against **this** shape, with a fake, before
// any engine exists.
//
// **Why an event carries no audio.** D118's answer is that the app never
// stores or uploads audio, and the mobile reconciliation's F26 turns it into a
// rule: STT is on-device (D44, D123), so no sample ever *needs* to cross a
// port. The strongest form of that rule is a shape: an `AudioIn` event has
// exactly one field, `kind`. `AUDIO_IN_EVENT_FIELDS` is asserted by this
// module's tests, so a later change that added a `samples`, `frames` or `pcm`
// field would fail a test rather than quietly start carrying speech through
// the app.
//
// **The three events, and which of them is the capture path's decision.**
//
//   `speech-start`  the capture path saw speech begin: VAD crossed its floor.
//   `speech-end`    the capture path decided the user's turn ended -- §5's
//                   trailing silence (~800 ms, extended when the transcript
//                   looks incomplete). End-of-turn detection is the capture
//                   path's job, which is why it is an event here and not a
//                   timer in `pipeline.js`.
//   `barge-in`      the user is speaking **and** the assistant's own audio is
//                   playing. Only a capture path with echo cancellation can
//                   tell those two apart (§5), so the signal is the device's.
//                   The pipeline does not depend on it alone: a
//                   `speech-start` that arrives while an utterance is playing
//                   is treated as the same thing, so a capture path without
//                   the signal still cannot be talked over.
//
// **D122 is why `start()` can be refused.** The phone's microphone is an OS
// permission with a usage-description string the owner writes, and the
// background audio mode is declared only if barge-in needs it; when the OS
// refuses either, the app degrades to a visible "tap to speak" control rather
// than pretending to listen. This port is where that refusal is **named**:
// `start()` answers `microphone-denied` or `background-audio-denied`, and the
// control the owner then sees is P3.5's screen, built on that answer. Refusing
// is a value and not a throw, because a phone that says no to a permission
// prompt is working as designed.
//
// **D123 is why there is no engine in this file.** The bake-off -- measure the
// platform's own on-device engines on the oldest supported device, local
// candidates only -- is owner-run check 85, scheduled in phase 5. Nothing in
// this task implements a capture path; `index.js`'s own test asserts that no
// file under `src/audio/` imports an SDK at all.

import { accept, refuse } from './refusal.js';

/** The refusal codes this port returns. */
export const AUDIO_IN_CODES = Object.freeze({
  /** D122: the OS refused the microphone. The app shows "tap to speak". */
  MICROPHONE_DENIED: 'microphone-denied',
  /** D122: the OS refused the background audio mode barge-in would need. */
  BACKGROUND_AUDIO_DENIED: 'background-audio-denied',
  /** A capture path that is not running cannot report speech. */
  NOT_RUNNING: 'not-running',
});

/** The events a capture path may emit. Nothing else is an event. */
export const AUDIO_IN_EVENTS = Object.freeze(['speech-start', 'speech-end', 'barge-in']);

/**
 * The exact fields an event carries. One field: the capture path says **what
 * happened**, and never what it heard (D118, F26 -- see the header).
 */
export const AUDIO_IN_EVENT_FIELDS = Object.freeze(['kind']);

/**
 * Whether `value` is an audio input port: the four methods, as functions.
 *
 * A shape check, deliberately shallow: it cannot promise that an engine
 * behaves, only that this is not the wrong object handed to the right
 * parameter -- which is the mistake that costs the most and is the cheapest
 * to catch.
 *
 * @param {unknown} value a candidate port
 * @returns {boolean}
 */
export function isAudioIn(value) {
  if (value === null || typeof value !== 'object') return false;
  return ['start', 'stop', 'running', 'on'].every((name) => typeof value[name] === 'function');
}

/**
 * The fake capture path: nothing is captured, and every event is scripted
 * (D52, D126 -- fakes from task one, so the voice pipeline's logic is testable
 * with no device, no microphone and no audio).
 *
 * **Deterministic, and honest.** There is no clock, no timer and no random
 * source in it: a test calls `speechStart()`, `speechEnd()` or `bargeIn()` and
 * the event is delivered synchronously. A stopped input emits nothing -- the
 * scripting methods refuse with `not-running` -- so a test cannot accidentally
 * assert against a microphone that was never started, which is the shape of
 * bug a fake is most likely to hide.
 *
 * @example
 *   const audioIn = new FakeAudioIn();
 *   audioIn.on('speech-end', () => turns++);
 *   await audioIn.start();
 *   audioIn.speechStart();
 *   audioIn.speechEnd();          // turns === 1
 */
export class FakeAudioIn {
  #refusal;
  #running = false;
  #handlers = new Map(AUDIO_IN_EVENTS.map((kind) => [kind, new Set()]));
  #emitted = [];
  #starts = 0;
  #stops = 0;

  /**
   * @param {object} [options]
   * @param {string | null} [options.refuse] the code `start()` refuses with,
   *   one of `AUDIO_IN_CODES`' two denial codes -- D122's degradation path.
   */
  constructor({ refuse: refusal = null } = {}) {
    if (refusal !== null && refusal !== AUDIO_IN_CODES.MICROPHONE_DENIED && refusal !== AUDIO_IN_CODES.BACKGROUND_AUDIO_DENIED) {
      throw new TypeError('refuse must be one of D122\'s two denial codes, or null');
    }
    this.#refusal = refusal;
  }

  /**
   * Ask the OS for the microphone and start capturing.
   *
   * `{ ok: true, changed }` -- `changed` is false when it was already running,
   * so a screen that starts twice is not an error. `{ ok: false, code }` when
   * the OS refused (D122).
   */
  async start() {
    this.#starts++;
    if (this.#refusal !== null) return refuse(this.#refusal, 'start');
    if (this.#running) return accept({ changed: false });
    this.#running = true;
    return accept({ changed: true });
  }

  /** Stop capturing. `changed` is false when it was not running. */
  async stop() {
    this.#stops++;
    if (!this.#running) return accept({ changed: false });
    this.#running = false;
    return accept({ changed: true });
  }

  /** Whether this input is capturing right now. */
  running() {
    return this.#running;
  }

  /**
   * Subscribe to one event kind. Returns the unsubscribe, which is idempotent.
   *
   * The returned value is always a function, so a bad kind or a handler that
   * is not a function **throws**: there is no runtime state in which a
   * subscription silently did not happen (see `refusal.js`).
   */
  on(kind, handler) {
    if (!AUDIO_IN_EVENTS.includes(kind)) throw new TypeError(`unknown event: ${kind}`);
    if (typeof handler !== 'function') throw new TypeError('a handler is a function');
    this.#handlers.get(kind).add(handler);
    return () => this.#handlers.get(kind).delete(handler);
  }

  // ---- the scripting surface: what only a fake has ----------------------

  /** The capture path saw speech begin. */
  speechStart() {
    return this.#emit('speech-start');
  }

  /** The capture path decided the turn ended (trailing silence). */
  speechEnd() {
    return this.#emit('speech-end');
  }

  /** The user is speaking while the assistant is speaking (echo-cancelled). */
  bargeIn() {
    return this.#emit('barge-in');
  }

  /** Every event this fake has delivered, in order, as frozen copies. */
  emitted() {
    return Object.freeze([...this.#emitted]);
  }

  /** How many times `start()` and `stop()` were asked for. */
  calls() {
    return Object.freeze({ starts: this.#starts, stops: this.#stops });
  }

  #emit(kind) {
    if (!this.#running) return refuse(AUDIO_IN_CODES.NOT_RUNNING, 'kind');
    const event = Object.freeze({ kind });
    this.#emitted.push(event);
    for (const handler of [...this.#handlers.get(kind)]) handler(event);
    return accept({ event });
  }
}
