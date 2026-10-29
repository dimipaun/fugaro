// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// Speaking, behind a port -- and the audio seam P3.6 stubs (mobile plan, Task
// P3.6; Global Constraint 10, Voice D52).
//
// **What is stubbed, and why.** P3.6's client has to speak the consumer's
// replies, and it has to know whether the speaking happened: the plan's own
// verification is that it "acks a message only after it has spoken it", which
// is a statement about a failed speak as much as a successful one. The audio
// interfaces are **P3.4** (`mobile/src/audio/`, PR #592), which P3.6 was
// written without: the audio module had no directory in the tree then, and this
// task may not depend on code that is not on `main`. **P3.4 has since merged
// with this change** -- `mobile/src/audio/` is the tree's now -- and **P3.5 has
// since wired the two together**: `src/voice/speaker.js`'s
// `createPipelineSpeaker({ pipeline })` is the adapter a built app hands this
// client, so this port is what P3.4's text-to-speech interface answers through.
// The port stays this module's own all the same -- the client is handed one
// object and never learns what is behind it, and merging the two into one would
// be a second way to speak. So what P3.6 defines here is the
// smallest seam the client's rule needs -- one async call that takes text and
// answers whether it was spoken -- and nothing about voices, queues, barge-in,
// latency or the platform's own players. Those are P3.4's (the interfaces) and
// P3.5's (the screen, the interrupt switch, and the wiring).
//
// The seam is deliberately narrower than an audio API on purpose: the client
// makes exactly one audio decision (do not ack what was not spoken), and a
// wider seam here would be a second place for the voice screen's rules to
// live.
//
// **What the real implementation owes this port.** `speak(text)` resolves
// `{ ok: true }` when the text has been spoken to the end, and a refusal when
// it was not -- an interrupted utterance, a missing audio route, a text the
// engine cannot voice. A refusal is not an error to log: the client's answer
// is to leave the message unacked, so the next pass tries again. Nothing in
// this module reads a clock, a device or a network.

/** A speak that did not happen, and the one input refusal. */
export const SPEAK_CODES = Object.freeze({
  /** There was nothing to speak. */
  BAD_TEXT: 'bad-text',
  /** The text was not spoken: interrupted, unrouted, or refused by the engine. */
  SPEAK_FAILED: 'speak-failed',
});

function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * The port's in-memory double: it records what it was asked to speak, and can
 * be told to fail, which is how "never ack what was not spoken" is tested
 * without a speaker.
 */
export class MemorySpeaker {
  #spoken = [];
  #fail;

  /** @param {{ fail?: boolean }} [options] `fail` makes every speak refuse. */
  constructor(options = {}) {
    this.#fail = options.fail === true;
  }

  /**
   * Speak one reply.
   *
   * @param {unknown} text
   * @returns {Promise<{ ok: true } | { ok: false, code: string, field: string }>}
   */
  async speak(text) {
    if (typeof text !== 'string' || text === '') return refuse(SPEAK_CODES.BAD_TEXT, 'text');
    if (this.#fail) return refuse(SPEAK_CODES.SPEAK_FAILED, 'text');
    this.#spoken.push(text);
    return Object.freeze({ ok: true });
  }

  /** What was actually spoken, in order. Test-only: a speaker has no memory. */
  spoken() {
    return Object.freeze([...this.#spoken]);
  }

  /** Turn the failure on or off, for the interrupted-utterance case. */
  setFailing(fail = true) {
    this.#fail = fail === true;
    return this;
  }
}
