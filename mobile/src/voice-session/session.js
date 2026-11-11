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
// **Six readings this task makes, each visible in the code below.**
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
//   * **A pause closes the microphone.** §4's two paused states are "mic closed,
//     playback paused": §6.2's `.began` is "stop capture and pause playback" and
//     §6.3's unsafe route is "must not continue speaking through the
//     loudspeaker". So entering one stops the screen -- which is P3.4's
//     `pipeline.stop()`, and cancels the utterance in flight -- and the resume
//     that ends it starts the screen again. This is what F24 and F9 rest on: a
//     microphone left open through a phone call transcribes the call, and the
//     screen would send it. A pause that arrives while `start()` is opening the
//     microphone is followed there too, so no path leaves one open in a pause.
//   * **A port call is not a transition.** `native.start()`, `native.setMuted()`
//     and the screen's own start answer when they answer, and the session can
//     move while they do. What those windows forbid is applying something
//     computed before them: a verdict reduced from a state the session has left,
//     a screen started for a session that is already over, a microphone opened
//     after the teardown that closed it, and a new session begun under a
//     teardown that is still running. Each is guarded where it would be applied
//     (`start`, `setMuted`), and each guard is tested.

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
  /** The microphone would not close for a pause (§4: "mic closed"). */
  PAUSE_FAILED: 'pause-failed',
  /** The microphone would not open again on the resume that ended a pause. */
  RESUME_FAILED: 'resume-failed',
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
  [SESSION_CODES.PAUSE_FAILED]: 'the microphone could not be closed for the pause',
  [SESSION_CODES.RESUME_FAILED]: 'the microphone could not be opened on resume',
});

/** One of those failures, frozen, in the shape §5's `error` carries. */
function failureFor(code) {
  return Object.freeze({ code, message: FAILURE_MESSAGES[code] });
}

/**
 * What arrives at this module from outside: §5's six kinds, which the native
 * port emits, and `tick`, which is time passing. Everything else the machine
 * reduces is this module's own to drive (see `NOT_INCOMING`).
 */
const INCOMING_EVENTS = Object.freeze([...SESSION_EVENTS, 'tick']);

/** The two states a session does not come back from without a new `start`. */
const OVER = Object.freeze(['ending', 'ended']);

/**
 * §4's two paused states, in both of which the microphone is closed.
 *
 * `paused_interruption` is a call, an alarm, Siri or another app taking the
 * audio; `paused_route` is headphones out or Bluetooth lost, held until a person
 * says otherwise. Neither is `ending` or `ended` -- those close the microphone
 * through the teardown -- and neither is `starting`, where the screen is started
 * once the port has answered.
 */
const PAUSED = Object.freeze(['paused_interruption', 'paused_route']);

/**
 * The two states §4's table has the microphone open in: the pair the machine
 * derives from the pipeline (`listen`), and the states a pause closes the screen
 * from. `starting` is deliberately not one of them -- the screen is started by
 * `start()` once the port has answered, and a pause that arrives before that
 * finds a microphone nobody has opened yet.
 */
const LIVE = Object.freeze(['listening', 'speaking']);

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
  /** Which session this object is holding. `start()` captures it and gives up
   * after each port call if it has moved, so a call the caller has abandoned
   * cannot drive the session that replaced it. */
  let generation = 0;
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
  /** The screen call a pause or a resume asked for, where `settle` waits for it. */
  let screenWork = null;
  /** The last failure this session recorded, on the snapshot the caller reads:
   * a teardown that did not finish, or a microphone that would not close or
   * open for a pause. */
  let failure = null;

  /**
   * Tell the port what the session is, if that is news (D217).
   *
   * The two silences are deliberate: nothing is pushed before the port answered
   * `start()` (there is no session on the other end to render), and nothing is
   * pushed for `ending` or `ended`, because `end(reason)` is that news with the
   * cause on it. Not awaited, and a rejection is dropped on purpose: a status a
   * screen draws is not worth blocking a transition on, and a port that could
   * not be told reports it itself (§5's `error` event arrives from the port, not
   * from this call). The handler is what keeps that silence from becoming an
   * unhandled rejection.
   */
  function push() {
    if (!begun || OVER.includes(current.state)) return;
    if (pushed.state === current.state && pushed.muted === current.muted) return;
    pushed = Object.freeze({ state: current.state, muted: current.muted });
    native
      .updateStatus({
        state: current.state,
        muted: current.muted,
        startedAt: current.startedAt,
      })
      .catch(() => {});
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
   * A failure anywhere in it is recorded on this module's own snapshot field and
   * the session ends anyway: a teardown that failed and left the session in
   * `ending` would be the live session §9 forbids, and the caller reads the
   * failure off the snapshot instead of off a rejection -- the state is the
   * answer, and a second channel would only be a second version of it. It is
   * recorded rather than sent as §5's `error` event because three of the four
   * paths into this teardown are already in `ended` when it runs, and a session
   * that is over takes no event.
   *
   * Each call has its own handler, rather than one around both: a screen that
   * would not stop is a microphone that may still be open, and it is no reason
   * to leave the port running -- the foreground service and the Live Activity --
   * for a session this module has already reported as over.
   */
  function startTeardown(reason) {
    if (closing) return teardown;
    closing = true;
    teardown = (async () => {
      try {
        await screen.stop();
      } catch {
        failure = failureFor(SESSION_CODES.END_FAILED);
      }
      if (begun) {
        try {
          await native.end(reason);
        } catch {
          failure = failureFor(SESSION_CODES.END_FAILED);
        }
      }
      // Only a hang-up waits for this event; the paths that reach `ended` on
      // their own are already there, and the machine refuses an `end` they
      // never asked for.
      if (current.state === 'ending') apply({ type: 'end' });
    })();
    return teardown;
  }

  /**
   * Close the microphone for §4's pause, and record it if it will not close.
   *
   * A rejection here is a microphone that may still be open, so it is not
   * swallowed; it is not §5's `error` event either, which would end the session
   * a pause is not ending. The caller reads it off the snapshot.
   */
  function closeTheMicrophone() {
    return queueScreenWork(async () => {
      try {
        await screen.stop();
      } catch {
        failure = failureFor(SESSION_CODES.PAUSE_FAILED);
      }
    });
  }

  /** Open it again on the resume that ended the pause, the same way. */
  function openTheMicrophone() {
    return queueScreenWork(async () => {
      try {
        await screen.start();
      } catch {
        failure = failureFor(SESSION_CODES.RESUME_FAILED);
      }
    });
  }

  /**
   * Run one screen call behind whatever screen call is already in flight, and
   * keep it where `settle` waits for it.
   *
   * A pause and the resume that ends it can arrive while the first call is still
   * running -- P3.4's `stop()` and `start()` both answer asynchronously -- and
   * the order they were asked for is the order the microphone has to see, so the
   * second waits for the first rather than racing it.
   */
  function queueScreenWork(work) {
    const previous = screenWork;
    const running = (async () => {
      if (previous !== null) await previous;
      await work();
    })();
    screenWork = running;
    return running;
  }

  /**
   * §4's two pauses are "mic closed, playback paused", and the resume that ends
   * one opens it again.
   *
   * §6.2's `.began` is "stop capture and pause playback" and §6.3's unsafe route
   * is "must not continue speaking through the loudspeaker"; P3.4's `stop()` is
   * both, since it cancels the utterance in flight as well as the capture. A
   * microphone left open through a phone call is the failure F24 is about: the
   * call is transcribed, and the screen sends it.
   *
   * Only the crossings between the pair above are acted on, so a mute, a second
   * interruption and a `tick` inside a pause -- none of which changes the state
   * -- reach neither the screen nor the microphone.
   */
  function followThePause(before, after) {
    if (LIVE.includes(before) && PAUSED.includes(after)) closeTheMicrophone();
    else if (PAUSED.includes(before) && LIVE.includes(after)) openTheMicrophone();
  }

  /** Adopt a state the machine answered with: the lease, the push, the pause,
   * and the teardown. */
  function commit(state) {
    const before = current.state;
    current = state;
    if (current.state === 'ended') giveBackTheLease();
    push();
    followThePause(before, current.state);
    if (begun && OVER.includes(current.state)) startTeardown(current.endReason);
  }

  /** Reduce one event, and keep the verdict's state when there is one. */
  function apply(event) {
    const verdict = machine.reduce(current, event, ctx);
    if (verdict.ok) commit(verdict.state);
    return verdict;
  }

  /** Wait for the work a transition asked for, so that a caller's `await` is the
   * whole transition: `ended` means the port was ended, and a paused session
   * means the microphone is closed. */
  async function settle() {
    const running = teardown;
    const working = screenWork;
    if (running !== null) await running;
    if (working !== null) await working;
  }

  /** A fresh session carries none of the last one's bookkeeping -- the machine
   * resets its own state the same way, and §8's "Tap to resume" starts a session
   * rather than continuing one. */
  function reset() {
    generation += 1;
    begun = false;
    closing = false;
    teardown = null;
    screenWork = null;
    released = false;
    pushed = UNPUSHED;
    failure = null;
  }

  return {
    /**
     * Ask for a session: the port first, then P3.5's screen, which is already
     * the one path to `pipeline.start()`.
     */
    async start() {
      // The last session's teardown is waited for before anything is reset. A
      // session that ends on its own (§8's timeout, §10's error, §9's stopped
      // service) does not wait for the port's `end()`, so a resume can meet a
      // teardown that is still running -- and a session begun under it would
      // have the last one's `screen.stop()` and `native.end()` land on it:
      // microphone closed, port ended, state still `listening`. `reset` clears
      // the record of that teardown, which is the only thing that could have
      // waited for it.
      await settle();
      // A session that is over starts fresh; a live one is the machine's own
      // refusal, and resetting for it would silence the session it is in.
      if (current.state === 'idle' || current.state === 'ended') reset();
      // The session this call is for. Another `start()` -- §8's "Tap to resume"
      // after a session that ended on its own -- can reset and begin while a
      // port call of this one is still in flight, and this call's late answer
      // belongs to the session that is gone: its rejection would end the new one
      // with §5's `error`, and its resolution would open a microphone and drive
      // a `started` for a session it did not ask for. Each await below is
      // checked against this, and a call that finds the session moved gives up
      // without touching it.
      const mine = generation;
      const verdict = apply({ type: 'start' });
      if (!verdict.ok) return verdict;

      try {
        await native.start({ title, resumeTimeoutMs });
      } catch {
        if (generation !== mine) return refuse(SESSION_CODES.START_FAILED);
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
      if (generation !== mine) return refuse(SESSION_CODES.START_FAILED);
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
      // A pause is the other thing that window can hold, and §4's paused states
      // are "mic closed": the screen is not started into one, so the microphone
      // is never opened inside the phone call that paused the session. The
      // session is alive and paused, and the port is told so; the resume that
      // ends the pause is what starts the screen (the machine refuses this
      // `started`, the session not being `starting` any more).
      if (PAUSED.includes(current.state)) return apply({ type: 'started' });
      // D122's degradation is the screen's (see the header): a microphone the
      // OS refused is shown there, and the session goes on without it. A capture
      // path that *rejects* is not that refusal -- the session cannot listen at
      // all -- so it ends the way §10's errors end one, with the port that began
      // ended behind it, rather than leaving the session in `starting`.
      try {
        await screen.start();
      } catch {
        if (generation !== mine) return refuse(SESSION_CODES.START_FAILED);
        apply({
          type: 'error',
          code: SESSION_CODES.START_FAILED,
          message: FAILURE_MESSAGES[SESSION_CODES.START_FAILED],
        });
        await settle();
        return refuse(SESSION_CODES.START_FAILED);
      }
      if (generation !== mine) return refuse(SESSION_CODES.START_FAILED);
      // The other end of the window `native.start()` opened: the session can end
      // while the microphone is being opened, and the teardown that ended it
      // stopped a screen that had not finished starting -- so the microphone it
      // went on to open is closed here, after the start it raced, and the caller
      // is told the session did not start.
      if (OVER.includes(current.state)) {
        try {
          await screen.stop();
        } catch {
          failure = failureFor(SESSION_CODES.END_FAILED);
        }
        await settle();
        return refuse(SESSION_CODES.START_FAILED);
      }
      // ...and it can be paused in that same window, which is the microphone
      // this start opened and a session that must not be listening: the pause
      // closed a screen that had not started yet.
      if (PAUSED.includes(current.state)) {
        closeTheMicrophone();
        await settle();
        return apply({ type: 'started' });
      }
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
     * P3.5's own -- which read the pipeline for `speaking` and `turn` at this
     * call rather than caching them, so the pair a screen draws can never be one
     * event old (D206, F3) -- and this module's own `failure`, which is where a
     * teardown that did not finish, or a microphone that would not close or open
     * for a pause, is read.
     */
    state() {
      return Object.freeze({ ...current, screen: screen.state(), failure });
    },
  };
}
