// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The scripted native port: a device's answers, with no device (voice-session
// plan, Task VS1.2; D213, D205, F7, F9).
//
// **The twin of `src/audio/`'s fakes, one level up.** `FakeAudioIn`, `FakeStt`
// and `FakeTts` are the reason P3.4's pipeline is testable before an engine
// exists; §5's port is the same problem one directory over, because the two
// platform halves are VS3's and VS4's and the session is written first. So this
// class answers the five methods with a recorded call and no device, and it
// carries the sixth -- the subscription -- with a way for a test to *inject*
// the event stream. That injection is what makes VS0's and VS1's tests
// device-free: a phone call, an unplugged headset, iOS's suspension
// notification and a stopped service are all one `emit(...)` here, and the
// whole of §11's matrix can be driven with the screen, the pipeline and the
// machine as they really are.
//
// **One validator, not two.** `emit` is `createNativeEvents()`'s `deliver`, not
// a second copy of it: an event that is not one of §5's six well-formed kinds
// is refused here with the same code and the same field the real port refuses
// it with (VS0.1's validator, `NATIVE_CODES.NOT_AN_EVENT`), and a refused one
// reaches no subscriber and is recorded nowhere -- nothing was delivered. That
// is D213's reporting shape and it is the fence beside it: a code and the field
// it names, never audio, never a transcript, and never a sentence the arrival
// brought with it (F9, F25).
//
// **The fake has no state of its own, and that is deliberate.** It does not
// remember whether a session is up, so it never refuses `start()` twice or
// `end()` before a `start()`: whether a call is legal is the machine's
// transition table, and a fake that guessed at it would be a second one.
// `FakeAudioIn` refuses its scripting methods while stopped because a
// microphone that heard something is the bug a fake is most likely to hide;
// here the equivalent trap is an event nobody was subscribed to -- and that is
// the test's to see, in the recorded calls and in `emitted()`.
//
// **Deterministic.** No clock, no timer, no random source, no device: every
// call answers on the next microtask and nothing happens between them that a
// test did not ask for. `FakeTts` holds an utterance open until the test ends
// it; nothing here needs that -- a port call that answers later is
// `session.test.js`'s own double, where the window between the ask and the
// answer is the subject.

import { createNativeEvents } from './port.js';

/**
 * §5's `VoiceSessionNative`, recorded and scripted.
 *
 * @example
 *   const native = new FakeVoiceSessionNative();
 *   native.on((event) => session.dispatch(event));
 *   await session.start();
 *   native.emit({ type: 'interruptionBegan', source: 'call' });
 *   native.calls().map((call) => call.name);
 */
export class FakeVoiceSessionNative {
  #events = createNativeEvents();
  #calls = [];
  #emitted = [];

  /**
   * §5's `start`: the port is asked for a session, and nothing on the device
   * changes. The options are recorded rather than judged -- the title and §8's
   * window are the caller's values, and `createVoiceSession` is what refuses a
   * session built without them.
   *
   * @param {{ title: string, resumeTimeoutMs?: number }} options
   */
  async start(options) {
    this.#record('start', [options]);
  }

  /**
   * §5's `updateStatus`: what the Live Activity and the notification would be
   * redrawn with.
   *
   * @param {{ state: string, muted: boolean, startedAt: number }} status
   */
  async updateStatus(status) {
    this.#record('updateStatus', [status]);
  }

  /**
   * §5's `setMuted`: the audio, muted or unmuted.
   *
   * @param {boolean} muted
   */
  async setMuted(muted) {
    this.#record('setMuted', [muted]);
  }

  /**
   * §5's `end`: the foreground service stops and the Live Activity ends, with
   * §4's reason on it.
   *
   * @param {string} reason one of `END_REASONS`
   */
  async end(reason) {
    this.#record('end', [reason]);
  }

  /**
   * §5's `cleanupStaleSessions`: §9's launch-time sweep of a session a kill
   * left behind.
   *
   * Nothing calls this one on the session's behalf: it is the app's, on every
   * launch, and the recorded calls are where "the session never sweeps" is
   * visible (VS5.1).
   */
  async cleanupStaleSessions() {
    this.#record('cleanupStaleSessions', []);
  }

  /**
   * Subscribe to the port's event stream. The unsubscribe is idempotent.
   *
   * @param {(event: object) => void} handler
   * @returns {() => boolean}
   * @throws {TypeError} when the handler is not a function
   */
  on(handler) {
    return this.#events.on(handler);
  }

  /**
   * Hand one event to the subscribers, as the device would.
   *
   * The event is the test's to write, and it is checked exactly as a platform's
   * would be: §5's six well-formed kinds are delivered, anything else is the
   * port's refusal and reaches nobody. What a test may inject is §5's contract
   * and no more -- there is no path here for a sample, a word the owner said or
   * a route this contract does not name (F9).
   *
   * @param {unknown} event one of §5's events
   * @returns {{ ok: true, event: object } | { ok: false, code: string, field: string | null }}
   */
  emit(event) {
    const delivered = this.#events.deliver(event);
    if (delivered.ok) this.#emitted.push(delivered.event);
    return delivered;
  }

  /** Every call this fake was given, in order, as frozen `{ name, args }`. */
  calls() {
    return Object.freeze([...this.#calls]);
  }

  /** Every event this fake delivered, in order, as the frozen copies the
   * subscribers were handed -- a refused arrival is not among them. */
  emitted() {
    return Object.freeze([...this.#emitted]);
  }

  #record(name, args) {
    this.#calls.push(Object.freeze({ name, args: Object.freeze(args) }));
  }
}
