// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The session controller: the object the app talks to (voice-session plan,
// Task VS1.1; D206, D217, D201, F3, F4, F24).
//
// **What this module is.** VS0.1's event contract, VS0.2's machine and P3.5's
// screen are three pieces that do not know about each other; this is the seam
// between them and the phone. The spec's §3 says the rest of the app talks to
// this and to nothing else, and that is the whole shape of the file: every
// event goes through `machine.reduce`, every port call the machine deliberately
// does not make happens here, and P3.5's screen is *read* rather than
// re-implemented (F4).
//
// **What it owns.** The transitions: `reduce` is called with a context whose
// clock and `resumeTimeoutMs` are the caller's (D117, D201) and whose
// `pipeline` is P3.4's own object, so the machine's `listening`/`speaking` are
// asked of the pipeline every time (D206, F3) and no copy of the pair is stored
// here either. The port calls: `native.start` when the app asks for a session,
// `updateStatus` on a state or mute change (D217), `setMuted`, and `end`. And
// the teardown, which is the one thing §4's table cannot express: `ending` is
// where the machine waits for `end`, and this module is what makes that event
// mean "native's `end()` resolved".
//
// **What it must not own.** P3.5's switch, queue, offline refusal and lease.
// The session calls `screen.start()`, `screen.stop()`, `screen.lease()`,
// `screen.releaseLease()` and asks `screen.state()` for the snapshot a render
// uses; it never calls the mailbox client, never reads a credential, and
// imports nothing outside this directory -- the module's own scan holds the
// last one (`index.test.js`, F24), and the reason is the phone call this
// feature exists for: while the phone is locked the secure store is unreadable,
// so a resume path that reached a credential would fail on exactly the
// interruption it is for.
//
// **No timer and no clock.** §8's three minutes is a `tick` the *caller*
// injects, exactly as the machine's clock is: this file starts nothing, and the
// scan forbids it the timer it would start one with. The caller that owns the
// interval is the app (VS1.3), which is also where §9's
// `cleanupStaleSessions()` is called from -- a launch-time sweep, not a
// session's.
//
// **Five readings this task makes, each visible in the code below.**
//
//   * **A refused microphone is not an error.** `screen.start()` is P3.4's
//     `pipeline.start()`, and D122's degradation belongs to the screen: the OS
//     refusing the microphone emits `unavailable`, the screen shows it, and the
//     session goes on -- ending it would throw away the one thing D122 keeps,
//     a phone that can still speak. The caller reads D122's answer off the
//     screen's snapshot, where P3.5 already put it.
//   * **The port's first push is the one after it began.** §5's payload is
//     `{ state, muted, startedAt }`, and a port that has not answered `start()`
//     has no session to render, so `idle → starting` is pushed once
//     `native.start()` has resolved and not before. The same rule at the other
//     end: `ending` and `ended` are never pushed, because `native.end(reason)`
//     is the port's own copy of "the session is over" -- with the cause on it,
//     which a state name is not.
//   * **The teardown belongs to `ended`, not to `ending`.** §4 reaches `ending`
//     on a hang-up and `ended` *directly* on §8's timeout, §10's error and §9's
//     stopped service; §8's own step 2 is "end the Live Activity / stop the
//     foreground service" for the timeout, so every path that ends a session
//     closes the microphone and ends the port. `begun` is what keeps that
//     honest: a port that never answered `start()` is never asked to `end`.
//   * **The mute asks the machine first.** A mute with no session is refused
//     rather than handed to a port that has none, and the audio is muted before
//     the flag and the status that render it -- so a port that would not mute
//     leaves the session saying what is true.
//   * **A port call is not a transition.** `native.start()` and
//     `native.setMuted()` answer when they answer, and the session can move
//     while they do. What that window forbids is applying something computed
//     before it: a verdict reduced from a state the session has left, or a
//     screen started for a session that is already over. Both are guarded where
//     they would be applied (`start`, `setMuted`), and both guards are tested.

import { REMOTE_ACTIONS, SESSION_EVENTS } from './events.js';

/**
 * The refusals this module answers with, in the shape this tree's modules
 * answer with (`SCREEN_CODES`, `MACHINE_CODES`).
 */
export const SESSION_CODES = Object.freeze({
  /**
   * No session is running after `start()`: the port would not begin, or the
   * session was over before it answered -- an event ended it while `start()`
   * was in flight, and the screen is then never started.
   */
  START_FAILED: 'start-failed',
  /** The teardown the machine asked for did not finish. */
  END_FAILED: 'end-failed',
  /**
   * An event only this module may drive. `start`, `started` and `end` are the
   * machine's own half of the contract, and a caller that dispatched one would
   * move the session without the port -- which is the state this module exists
   * to keep it out of.
   */
  NOT_INCOMING: 'not-incoming',
});

/**
 * The sentence each of this module's own failures carries. §5's `error` event
 * is `{ code, message }`, and the code is the reporting -- D213's recommendation
 * is a code and the field it names, and never free text -- so the message is
 * this module's own words rather than a rejection's: a native port's string is
 * not this vocabulary, and §5's field is not a place to put one.
 */
const FAILURE_MESSAGES = Object.freeze({
  [SESSION_CODES.START_FAILED]: 'the voice session could not start',
  [SESSION_CODES.END_FAILED]: 'the voice session could not tear down',
});

/**
 * What arrives at this module from outside: §5's six kinds, which the native
 * port emits, and `tick`, which is time passing. Everything else the machine
 * reduces is this module's own to drive (see `NOT_INCOMING`).
 */
const INCOMING_EVENTS = Object.freeze([...SESSION_EVENTS, 'tick']);

/** The two states a session does not come back from without a new `start`. */
const OVER = Object.freeze(['ending', 'ended']);

/** Nothing has been pushed yet: the throttle's own starting point (D217). */
const UNPUSHED = Object.freeze({ state: null, muted: null });

/** A frozen refusal, in the shape this repository's modules answer with. */
function refuse(code, field = null) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * Build the session.
 *
 * Everything is a port or a value -- the machine, P3.5's screen, P3.4's
 * pipeline, §5's native port (VS1.2), the clock (D117) and §8's window (D201) --
 * and a missing or wrongly-shaped one **throws**, because a session built
 * without them is a session that fails at the first thing the owner says. That
 * is the rule `createVoicePipeline` and `createVoiceScreen` both hold, and this
 * is the same boundary one level up.
 *
 * @param {object} options
 * @param {object} options.machine VS0.2's machine: `reduce` and
 *   `INITIAL_SESSION_STATE`, injected so that this file imports no neighbour
 * @param {object} options.screen P3.5's voice screen (`createVoiceScreen`)
 * @param {object} options.pipeline P3.4's pipeline (`isVoicePipeline`)
 * @param {object} options.native §5's `VoiceSessionNative` (VS1.2)
 * @param {() => number} options.now the injected clock, in epoch ms (D117)
 * @param {number} options.resumeTimeoutMs §8's window. Required, and never
 *   defaulted here: the default is D201, which is the owner's decision and
 *   still open, so this module carries the caller's number and invents none
 * @param {string} options.title what the Live Activity and the notification
 *   are named, handed to the port unchanged
 * @returns {object} the session
 * @throws {TypeError} for a port, a value or a setting that cannot be used
 */
export function createVoiceSession({
  machine,
  screen,
  pipeline,
  native,
  now,
  resumeTimeoutMs,
  title,
} = {}) {
  if (
    machine === null ||
    typeof machine !== 'object' ||
    typeof machine.reduce !== 'function' ||
    machine.INITIAL_SESSION_STATE === null ||
    typeof machine.INITIAL_SESSION_STATE !== 'object'
  ) {
    throw new TypeError('machine is the session machine: reduce and INITIAL_SESSION_STATE');
  }
  if (
    screen === null ||
    typeof screen !== 'object' ||
    ['start', 'stop', 'state', 'lease', 'releaseLease'].some((name) => typeof screen[name] !== 'function')
  ) {
    throw new TypeError("screen is P3.5's voice screen: start, stop, state, lease and releaseLease");
  }
  // Both halves of the pair are required and neither is a convenience: they are
  // what the machine and P3.5's screen read, and a caller with a flag of its own
  // has built the second source of truth D206 forbids.
  if (
    pipeline === null ||
    typeof pipeline !== 'object' ||
    typeof pipeline.speaking !== 'function' ||
    typeof pipeline.turn !== 'function'
  ) {
    throw new TypeError("pipeline is P3.4's pipeline: speaking() and turn() (D206)");
  }
  if (
    native === null ||
    typeof native !== 'object' ||
    ['start', 'updateStatus', 'setMuted', 'end'].some((name) => typeof native[name] !== 'function')
  ) {
    throw new TypeError("native is §5's VoiceSessionNative: start, updateStatus, setMuted and end");
  }
  if (typeof now !== 'function') throw new TypeError('now is the injected clock (D117)');
  if (typeof resumeTimeoutMs !== 'number' || !Number.isFinite(resumeTimeoutMs) || resumeTimeoutMs < 0) {
    throw new TypeError("resumeTimeoutMs is a non-negative number (§8; D201 is the caller's decision)");
  }
  if (typeof title !== 'string' || title.length === 0) throw new TypeError('title is a non-empty string');

  /** The machine's context: the caller's clock and window, and the pipeline's
   * own object, so `listening`/`speaking` are derived and never stored (D206). */
  const ctx = Object.freeze({ now, resumeTimeoutMs, pipeline });

  let current = machine.INITIAL_SESSION_STATE;
  /** Whether the port answered `start()`: the gate on every push, and on `end`. */
  let begun = false;
  /** The last state and mute the port was told about, so a render is throttled. */
  let pushed = UNPUSHED;
  /** The lease is given back once, however many ways the session ends. */
  let released = false;
  /** The teardown, once one has started. `closing` is set before it exists, so
   * the paths that reach it from inside itself cannot start a second one. */
  let closing = false;
  let teardown = null;

  /**
   * Tell the port what the session is, if that is news (D217).
   *
   * The two silences are deliberate: nothing is pushed before the port answered
   * `start()` (there is no session on the other end to render), and nothing is
   * pushed for `ending` or `ended`, because `end(reason)` is that news with the
   * cause on it. Not awaited: a status a screen draws is not worth blocking a
   * transition on, and a push that failed is the port's own to report (§5's
   * `error` event arrives from it, not from this call).
   */
  function push() {
    if (!begun || OVER.includes(current.state)) return;
    if (pushed.state === current.state && pushed.muted === current.muted) return;
    pushed = Object.freeze({ state: current.state, muted: current.muted });
    void native.updateStatus({
      state: current.state,
      muted: current.muted,
      startedAt: current.startedAt,
    });
  }

  /**
   * Give P1.2's lease back, once.
   *
   * Nothing is given back that was never taken: a screen whose lease state has
   * not been adopted refuses a release, and that refusal is not this module's
   * to make. A lease another consumer holds is P3.5's to surface rather than
   * ignore, and its snapshot carries that (F4).
   */
  function giveBackTheLease() {
    if (released) return;
    released = true;
    const claim = screen.lease();
    if (!claim.ok || claim.lease === null) return;
    screen.releaseLease();
  }

  /**
   * The teardown the machine waits on: the microphone closes, the port ends,
   * and only then is the session over.
   *
   * A failure anywhere in it is reported as §5's `error` event with this
   * module's own code, and the session ends anyway: a teardown that failed and
   * left the session in `ending` would be the live session §9 forbids, and the
   * caller reads the failure off the snapshot instead of off a rejection --
   * the state is the answer, and a second channel would only be a second
   * version of it.
   */
  function startTeardown(reason) {
    if (closing) return teardown;
    closing = true;
    teardown = (async () => {
      try {
        await screen.stop();
        if (begun) await native.end(reason);
      } catch {
        apply({ type: 'error', code: SESSION_CODES.END_FAILED, message: FAILURE_MESSAGES[SESSION_CODES.END_FAILED] });
        return;
      }
      // Only a hang-up waits for this event; the paths that reach `ended` on
      // their own are already there, and the machine refuses an `end` they
      // never asked for.
      if (current.state === 'ending') apply({ type: 'end' });
    })();
    return teardown;
  }

  /** Adopt a state the machine answered with: the lease, the push, the teardown. */
  function commit(state) {
    current = state;
    if (current.state === 'ended') giveBackTheLease();
    push();
    if (begun && OVER.includes(current.state)) startTeardown(current.endReason);
  }

  /** Reduce one event, and keep the verdict's state when there is one. */
  function apply(event) {
    const verdict = machine.reduce(current, event, ctx);
    if (verdict.ok) commit(verdict.state);
    return verdict;
  }

  /** Wait for a teardown the machine asked for, so that a caller's `await` is
   * the whole transition: `ended` means the port was ended. */
  async function settle() {
    const running = teardown;
    if (running !== null) await running;
  }

  /** A fresh session carries none of the last one's bookkeeping -- the machine
   * resets its own state the same way, and §8's "Tap to resume" starts a session
   * rather than continuing one. */
  function reset() {
    begun = false;
    closing = false;
    teardown = null;
    released = false;
    pushed = UNPUSHED;
  }

  return {
    /**
     * Ask for a session: the port first, then P3.5's screen, which is already
     * the one path to `pipeline.start()`.
     */
    async start() {
      // A session that is over starts fresh; a live one is the machine's own
      // refusal, and resetting for it would silence the session it is in.
      if (current.state === 'idle' || current.state === 'ended') reset();
      const verdict = apply({ type: 'start' });
      if (!verdict.ok) return verdict;

      try {
        await native.start({ title, resumeTimeoutMs });
      } catch {
        // The session the machine has to be told about is §5's `error` event --
        // it is what ends the session rather than leaving it in `starting` --
        // and the answer to the caller is this module's own code, because
        // "did the session start" is the question `start()` was asked.
        apply({
          type: 'error',
          code: SESSION_CODES.START_FAILED,
          message: FAILURE_MESSAGES[SESSION_CODES.START_FAILED],
        });
        return refuse(SESSION_CODES.START_FAILED);
      }
      begun = true;
      // ...and what happened while the port was answering can be the end of the
      // session: an event reduced in that window (an error, a stopped service, a
      // hang-up) tore down nothing, because `begun` was still false. A screen
      // started now would open a microphone for a session that is already over,
      // with no port left to end -- so the screen is not started, and the port
      // that did begin is ended with the reason the session carries. The
      // caller's question -- did the session start -- is answered with no; the
      // state says why.
      if (OVER.includes(current.state)) {
        startTeardown(current.endReason);
        await settle();
        return refuse(SESSION_CODES.START_FAILED);
      }
      push();
      // D122's degradation is the screen's (see the header): a microphone the
      // OS refused is shown there, and the session goes on without it.
      await screen.start();
      return apply({ type: 'started' });
    },

    /**
     * Hand the session one event from outside -- §5's kinds, and `tick`.
     *
     * The verdict is the machine's own, and the session has already done
     * whatever the transition asked for by the time it answers.
     */
    async dispatch(event) {
      if (
        event === null ||
        typeof event !== 'object' ||
        Array.isArray(event) ||
        !INCOMING_EVENTS.includes(event.type)
      ) {
        return refuse(SESSION_CODES.NOT_INCOMING, 'type');
      }
      const verdict = apply(event);
      await settle();
      return verdict;
    },

    /** §4: a hang-up, from the app, the Live Activity or the notification. */
    async hangUp() {
      const verdict = apply({ type: 'hangUp' });
      await settle();
      return verdict;
    },

    /** §4: a route pause resumes only on an explicit action, and this is it. */
    async resume() {
      const verdict = apply({ type: 'resume' });
      await settle();
      return verdict;
    },

    /** §8's three minutes: time passed, injected by whoever owns the interval. */
    async tick() {
      const verdict = apply({ type: 'tick' });
      await settle();
      return verdict;
    },

    /**
     * Mute or unmute, from the app's own control or from §5's `remoteAction`.
     *
     * The machine is asked first so a mute with no session is refused rather
     * than handed to a port that has none; the audio is muted before the flag
     * and the status that render it, so a port that would not mute leaves the
     * session saying what is true.
     */
    async setMuted(muted) {
      if (typeof muted !== 'boolean') throw new TypeError('muted is a boolean');
      const action = muted ? REMOTE_ACTIONS.MUTE : REMOTE_ACTIONS.UNMUTE;
      const from = current;
      const verdict = machine.reduce(from, { type: 'remoteAction', action }, ctx);
      if (!verdict.ok) return verdict;
      await native.setMuted(muted);
      // The port was muted, and the session may have moved while it was: that
      // verdict was reduced from a state the session has left, and committing it
      // would roll the move back -- a hang-up answered with `listening` again,
      // an interruption pause stripped of the reason and the instant §8's
      // timeout is measured from. The mute is reduced again where the session is
      // now, and a session that has ended refuses it rather than being reopened
      // by a flag: what the port did is the port's, and the state is the
      // machine's.
      if (current !== from) return apply({ type: 'remoteAction', action });
      commit(verdict.state);
      return verdict;
    },

    /**
     * Everything a render needs, in one frozen snapshot: the machine's fields,
     * and P3.5's own -- which read the pipeline for `speaking` and `turn` at
     * this call rather than caching them, so the pair a screen draws can never
     * be one event old (D206, F3).
     */
    state() {
      return Object.freeze({ ...current, screen: screen.state() });
    },
  };
}
