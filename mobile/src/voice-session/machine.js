// The session machine: a pure reducer over the spec's §5 event stream
// (voice-session plan, Task VS0.2; D205, D206, D201, D208, P8, F3, F9).
//
// **What the machine owns, and what it does not.** `mobile/src/voice-session/`
// owns the *session*: is there one, is it paused and why, and what ended it.
// P3.4's pipeline keeps the *turn* -- whose words these are, whether an
// utterance is in flight -- and the two meet in exactly one place: `ctx.pipeline`
// is read for `speaking()` and nothing is stored from it (F3, D206). A stored
// copy of `speaking` would make P3.5's `interruptDecision` judge a flag one
// event stale, and the failure that produces -- an update spoken over the user
// -- is the failure the voice screen exists to prevent. So `listening` and
// `speaking` here are **derived**: every call that enters the pair asks the
// pipeline, and a `tick` re-asks.
//
// **The machine is a function, and it touches nothing.** `reduce(state, event,
// ctx)` answers with a verdict and a new frozen state. It reads no clock (the
// clock is `ctx.now`, D117's server-anchored one, injected exactly as `now` is
// in `src/audio/`), starts no timer (§8's three minutes is a `tick` the caller
// injects, so a test can put the machine past its timeout without waiting), and
// calls nothing: `pipeline.start()`, `pipeline.stop()`, the native module's
// `start`/`updateStatus`/`setMuted`/`end` and P3.5's screen all belong to the
// controller (VS1.1, not written yet). What this file is, is the table §4
// writes -- every state, every event, and the reason each pair has no
// transition -- written down where a test can walk all of it.
//
// **The verdict, and the two kinds of failure.** A transition is
// `{ ok: true, state }` with a frozen state; no transition is
// `{ ok: false, code, field }` in the shape this tree's modules already answer
// with (`src/audio/refusal.js`), and the caller keeps the state it had. Three
// codes: `unknown-event` (a type that is not in the machine's vocabulary -- a
// platform's own event, arriving from a native port that was changed on one
// side), `malformed-event` (one of §5's kinds with a field missing or outside
// its vocabulary -- VS0.1's validator says no), and `no-transition` (a real
// event, in a state the table gives it no entry for). A **wiring bug** is still
// a throw, as it is everywhere in this tree: a state the machine could not have
// produced, a `ctx` without its three injected things, or a `pipeline` that is
// not the pipeline. Those have no runtime state to report -- they are a caller
// that will never work -- and they fail at the call rather than at the next
// phone call.
//
// **The six app-side events, beside §5's six.** The contract is native→JS; the
// machine is also driven by what the *app* does, and by time. `start` (the app
// asks for a session), `started` (the native port answered: the session is up),
// `hangUp` (the app's own hang-up -- §4 gives it one rule with the Live
// Activity's and the notification's, and `remoteAction` with `hangUp` is
// reduced by that same rule), `resume` (§4: a route pause resumes only on an
// explicit action), `end` (the teardown the machine asked for has finished:
// native's `end()` resolved), and `tick` (time passed). They carry no fields.
//
// **The rules that are not §4's table, and where each comes from.**
//
//   * **`shouldResume: false` ends the session as `interruption_timeout`.**
//     That is the spec's own sentence -- §4's bullet gives both ways out of
//     `paused_interruption` the one reason -- and it is kept rather than
//     improved on: "the interruption did not give the session back" is the
//     fact, and the timeout is what it means. A reviewer who wants a second
//     reason has one line to change and one test to move.
//   * **`shouldResume: true` after the timeout has already passed still ends
//     it.** §4's condition is *and elapsed time ≤ `resumeTimeoutMs`*, so an
//     `interruptionEnded` that arrives late is not a resume even if no `tick`
//     was injected in between.
//   * **A suspended interruption is not an interruption** (P8): iOS sends an
//     interruption notification when it suspends an app and the app runs again
//     (`AVAudioSessionInterruptionWasSuspendedKey`), so `interruptionBegan`
//     with `suspended: true` changes nothing at all -- no pause, in any state.
//     Without that rule the app reports a call that never happened.
//   * **An unsafe route outranks an interruption pause.** §4 has one pause that
//     resumes on its own and one that waits for a person; a route that became
//     unsafe while a call was in progress must not be resumed by the call
//     ending, so the pause becomes `paused_route` and its timeout stops
//     applying. §10's *connect* is the other half: `unsafe: false` never pauses
//     and never resumes -- a route pause resumes only on `resume`.
//   * **The first end reason is kept.** A teardown reports itself: the hang-up
//     that stopped the Android service comes back as `serviceStopped`, and
//     rewriting the reason would answer "why did this end" with the consequence
//     instead of the cause.
//   * **A second interruption does not restart the timer.** §8 starts it on
//     `interruptionBegan`; a Siri invocation during a call would otherwise
//     extend the window a paused session may hold a microphone open in.
//   * **`error` ends the session as §10 says**: `permission` for a revoked
//     permission, `error` for anything else. The event's own `code` and
//     `message` are kept on the state because **§5 carries them** -- the
//     contract's shape is not this machine's to narrow -- and never audio and
//     never a transcript. D213 (what a failure reports, and to where) is *open*
//     and gates VS1.2, so nothing here is built on it: its **recommended**
//     answer is a refusal code and the field it names, rendered by the screen
//     that made the call, and whether `message` is shown at all is that task's
//     to decide. What this file promises is only that the event's own two
//     fields survive the reduction.
//
// **`serviceStopped` is reduced here, and its emitter is not.** §5 carries the
// event and VS3.3 is the task that makes Android produce it (the self-stop and
// the launch cleanup); the machine's rule for it is one line -- the session is
// over, and the reason is the one it already had -- because a machine that left
// a dead session reading `listening` would be wrong in the meantime.

import {
  END_REASONS,
  PAUSE_REASONS,
  REMOTE_ACTIONS,
  SESSION_EVENTS,
  SESSION_STATES,
  isVoiceSessionEvent,
} from './events.js';

/**
 * The machine's whole vocabulary: §5's six kinds, and the six events the app
 * and the clock drive it with. A type outside this list is `unknown-event`.
 */
export const MACHINE_EVENTS = Object.freeze([
  ...SESSION_EVENTS,
  'start',
  'started',
  'hangUp',
  'resume',
  'end',
  'tick',
]);

/** The refusals `reduce` answers with. */
export const MACHINE_CODES = Object.freeze({
  /** A type that is not in `MACHINE_EVENTS` at all. */
  UNKNOWN_EVENT: 'unknown-event',
  /** One of §5's kinds, with a field missing or outside its vocabulary. */
  MALFORMED_EVENT: 'malformed-event',
  /** A real event, in a state §4's table gives it no transition for. */
  NO_TRANSITION: 'no-transition',
});

/**
 * A session that has not started: the state `reduce` is first called with.
 *
 * Frozen, and safe to hand to every caller: `reduce` never changes a state it
 * was given -- it builds a new frozen one -- so two sessions that started from
 * this value cannot affect each other.
 *
 * @typedef {object} SessionState
 * @property {string} state one of `SESSION_STATES`
 * @property {boolean} muted §4's orthogonal flag: it changes no state
 * @property {string|null} pauseReason one of `PAUSE_REASONS`, while paused --
 *   and `null` in every state that is not one of §4's two paused states: an
 *   ending or ended session carries no pause (`over`)
 * @property {string|null} pauseSource the interruption's `source`, while paused
 *   -- cleared with `pauseReason`
 * @property {string|null} route the last route the machine heard (§6.3)
 * @property {number|null} pausedAt `ctx.now()` when the **interruption** pause
 *   began; §8's timer is measured from it, a route pause has none, and neither
 *   has a session that is ending or over
 * @property {number|null} startedAt `ctx.now()` when the session was asked for
 * @property {string|null} endReason one of `END_REASONS`: **why the session is
 *   ending or has ended**, set when the end begins (a hang-up writes `hangup`
 *   while the state is still `ending`, so the teardown that follows reports
 *   itself without rewriting the cause -- see the header)
 * @property {{code: string, message: string}|null} error the last `error` event
 */
export const INITIAL_SESSION_STATE = Object.freeze({
  state: 'idle',
  muted: false,
  pauseReason: null,
  pauseSource: null,
  route: null,
  pausedAt: null,
  startedAt: null,
  endReason: null,
  error: null,
});

/** A frozen refusal, in the shape this repository's modules answer with. */
function refuse(code, field = null) {
  return Object.freeze({ ok: false, code, field });
}

/** A frozen acceptance carrying the state the session is in now. */
function ok(state) {
  return Object.freeze({ ok: true, state });
}

/** The state the session moves to: this one with those fields changed, frozen. */
function next(state, changes) {
  return Object.freeze({ ...state, ...changes });
}

/** §4's table has no entry for this event in this state. */
function no(event) {
  return refuse(MACHINE_CODES.NO_TRANSITION, event.type);
}

/**
 * `listening` or `speaking`, asked of the pipeline every time (D206): the
 * machine holds no copy of either, so `interruptDecision` can never be handed a
 * stale one.
 */
function listen(ctx) {
  return ctx.pipeline.speaking() ? 'speaking' : 'listening';
}

/** How long the current interruption pause has lasted, in `ctx.now`'s units. */
function pausedFor(state, ctx) {
  return ctx.now() - state.pausedAt;
}

/**
 * A session on its way out, or already over: **no pause survives it**.
 *
 * `pauseReason`/`pauseSource`/`pausedAt` answer "why is it paused *now*" -- §4
 * has exactly two paused states, and `ending` and `ended` are not among them --
 * so every transition that ends a session drops them, whichever way it ended: a
 * hang-up, §8's timeout, §4's `shouldResume: false`, §10's error and §9's
 * stopped service. Without this, an ended session would still answer "why is it
 * paused" with the call that is over, and D216's status word would read
 * "Paused — on a call" about a session that has none.
 *
 * @param {SessionState} state
 * @param {object} changes the fields this ending transition adds
 */
function over(state, changes) {
  return next(state, { pauseReason: null, pauseSource: null, pausedAt: null, ...changes });
}

/**
 * The session is over, with the first reason it was given (see the header).
 *
 * @param {SessionState} state
 * @param {string} reason one of `END_REASONS`
 */
function ended(state, reason) {
  return ok(over(state, { state: 'ended', endReason: state.endReason ?? reason }));
}

/** §10: a revoked permission ends as `permission`; every other error as `error`. */
function endReasonForError(code) {
  return code === END_REASONS.PERMISSION ? END_REASONS.PERMISSION : END_REASONS.ERROR;
}

/** The app asked for a session: `idle` or `ended` → `starting`. */
function onStart(state, event, ctx) {
  // A session already in flight is not started twice, and one that is tearing
  // down is not started again behind the teardown.
  if (state.state !== 'idle' && state.state !== 'ended') return no(event);
  // A fresh session carries none of the last one's flags (§8's "Tap to resume"
  // starts a session, not a continuation).
  return ok(next(INITIAL_SESSION_STATE, { state: 'starting', startedAt: ctx.now() }));
}

/** The native port answered: the session is up, so it listens. */
function onStarted(state, event, ctx) {
  if (state.state !== 'starting') return no(event);
  return ok(next(state, { state: listen(ctx) }));
}

/**
 * Hang up, from the app or from outside it (§4: one rule for the app, the Live
 * Activity and the notification). The session tears down: `ending` is where
 * `end` -- native's `end()` resolving -- finishes it.
 */
function onHangUp(state, event, ctx) {
  if (state.state === 'idle') return no(event); // there is no session to hang up
  // Already tearing down or already over: a second hang-up is the same hang-up,
  // not an error (the Android actions are handled in the service and are
  // idempotent for exactly this reason, VS3.2).
  if (state.state === 'ending' || state.state === 'ended') return ok(state);
  return ok(over(state, { state: 'ending', endReason: END_REASONS.HANGUP }));
}

/** The teardown finished: `ending` → `ended`. */
function onEnd(state, event) {
  if (state.state !== 'ending') return no(event);
  return ok(over(state, { state: 'ended' }));
}

/**
 * §4: a route pause resumes **only** on an explicit action. There is no timeout
 * on it and no event that resumes it on the user's behalf -- that is the whole
 * difference between the two pauses.
 */
function onResume(state, event, ctx) {
  if (state.state !== 'paused_route') return no(event);
  return ok(next(state, { state: listen(ctx), pauseReason: null, pauseSource: null, pausedAt: null }));
}

/**
 * Time passed. The only thing in this machine that a clock can do: end an
 * interruption pause that outlived §8's limit, and re-read the pipeline for
 * `listening` ⇄ `speaking`.
 */
function onTick(state, event, ctx) {
  if (state.state === 'paused_interruption' && pausedFor(state, ctx) > ctx.resumeTimeoutMs) {
    return ok(over(state, { state: 'ended', endReason: END_REASONS.INTERRUPTION_TIMEOUT }));
  }
  if (state.state === 'listening' || state.state === 'speaking') {
    return ok(next(state, { state: listen(ctx) }));
  }
  return ok(state);
}

/** §4: `starting | listening | speaking → paused_interruption`. */
function onInterruptionBegan(state, event, ctx) {
  // P8, first and in every state: a suspended-and-resumed app gets an
  // interruption notification that is not an interruption, and acting on it
  // reports a call that never happened.
  if (event.suspended === true) return ok(state);
  if (state.state === 'idle' || state.state === 'ending' || state.state === 'ended') return no(event);
  if (state.state === 'paused_route') {
    // A route pause is not an interruption pause: `interruptionEnded` resumes
    // on its own, and resuming onto a route the user has not accepted is the
    // surprise §6.3 exists to prevent.
    return no(event);
  }
  if (state.state === 'paused_interruption') {
    // A second interruption updates what took the audio and keeps the timer
    // running (§8's timer starts on `interruptionBegan`, and restarting it
    // would extend the window a paused session may hold open).
    return ok(next(state, { pauseSource: event.source }));
  }
  return ok(
    next(state, {
      state: 'paused_interruption',
      pauseReason: PAUSE_REASONS.INTERRUPTION,
      pauseSource: event.source,
      pausedAt: ctx.now(),
    }),
  );
}

/** §4: back to `listening` on `shouldResume: true` within the timeout. */
function onInterruptionEnded(state, event, ctx) {
  if (state.state !== 'paused_interruption') return no(event);
  if (event.shouldResume === false) {
    return ok(over(state, { state: 'ended', endReason: END_REASONS.INTERRUPTION_TIMEOUT }));
  }
  // §4's condition is `and elapsed time ≤ resumeTimeoutMs`: an interruption that
  // ends after the limit has passed is not a resume, whether or not a `tick`
  // arrived first. §8: "Never resume talking or listening on our own after the
  // timeout."
  if (pausedFor(state, ctx) > ctx.resumeTimeoutMs) {
    return ok(over(state, { state: 'ended', endReason: END_REASONS.INTERRUPTION_TIMEOUT }));
  }
  return ok(next(state, { state: listen(ctx), pauseReason: null, pauseSource: null, pausedAt: null }));
}

/** §6.3/§10: an unsafe route pauses; a safe one is recorded and continued on. */
function onRouteChanged(state, event, ctx) {
  if (state.state === 'idle' || state.state === 'ending' || state.state === 'ended') return no(event);
  if (event.unsafe === false) {
    return ok(next(state, { route: event.route }));
  }
  // Unsafe: the session must not play out loud, and it must not resume on its
  // own, so this pause is the one that waits for a person -- even if it arrived
  // during an interruption (see the header).
  return ok(
    next(state, {
      state: 'paused_route',
      pauseReason: PAUSE_REASONS.ROUTE,
      pauseSource: null,
      pausedAt: null,
      route: event.route,
    }),
  );
}

/** §6.5/§7.2: the three controls that exist outside the app. */
function onRemoteAction(state, event, ctx) {
  // §4's hang-up rule, whichever surface asked: the app, the Live Activity, the
  // notification.
  if (event.action === REMOTE_ACTIONS.HANG_UP) return onHangUp(state, event, ctx);
  if (state.state === 'idle' || state.state === 'ended') return no(event);
  // §10: "Mute pressed during interruption -- update flag; applies on resume."
  // The flag is orthogonal to the state and changes none of it.
  return ok(next(state, { muted: event.action === REMOTE_ACTIONS.MUTE }));
}

/** §9: the platform stopped the service this session was running in. */
function onServiceStopped(state, event) {
  // No session to end (the launch cleanup's orphan service is VS5.1's), or one
  // that has already ended.
  if (state.state === 'idle' || state.state === 'ended') return ok(state);
  return ended(state, END_REASONS.SERVICE_STOPPED);
}

/** §10: an error ends the session, and §5's own `code` and `message` are kept (D213 is open: VS1.2 decides what the screen shows). */
function onError(state, event) {
  if (state.state === 'ended') return ok(state);
  const error = Object.freeze({ code: event.code, message: event.message });
  if (state.state === 'idle') return ok(next(state, { error }));
  return ended(next(state, { error }), endReasonForError(event.code));
}

/** Every event the machine reduces, by type. */
const HANDLERS = Object.freeze({
  start: onStart,
  started: onStarted,
  hangUp: onHangUp,
  end: onEnd,
  resume: onResume,
  tick: onTick,
  interruptionBegan: onInterruptionBegan,
  interruptionEnded: onInterruptionEnded,
  routeChanged: onRouteChanged,
  remoteAction: onRemoteAction,
  serviceStopped: onServiceStopped,
  error: onError,
});

/**
 * Reduce one event into the session's next state.
 *
 * @param {SessionState} state the state the last call answered with
 * @param {object} event one of `MACHINE_EVENTS`
 * @param {object} ctx
 * @param {() => number} ctx.now the injected clock, in epoch ms (D117)
 * @param {number} ctx.resumeTimeoutMs §8's limit, the caller's (D201, open)
 * @param {object} ctx.pipeline P3.4's pipeline: the only source of `speaking`
 *   and `turn` (F3, D206)
 * @returns {{ ok: true, state: SessionState } | { ok: false, code: string, field: string|null }}
 * @throws {TypeError} for a state, a `ctx` or a pipeline the machine could not
 *   have been handed -- a wiring bug, and not a phone's event
 */
export function reduce(state, event, ctx) {
  if (state === null || typeof state !== 'object' || !SESSION_STATES.includes(state.state)) {
    throw new TypeError('state is a session state (SESSION_STATES)');
  }
  if (ctx === null || typeof ctx !== 'object') throw new TypeError('ctx is { now, resumeTimeoutMs, pipeline }');
  if (typeof ctx.now !== 'function') throw new TypeError('ctx.now is the injected clock (D117)');
  if (typeof ctx.resumeTimeoutMs !== 'number' || !Number.isFinite(ctx.resumeTimeoutMs) || ctx.resumeTimeoutMs < 0) {
    throw new TypeError('ctx.resumeTimeoutMs is a non-negative number (§8, D201)');
  }
  // Both halves are required, and neither is a convenience: `speaking()` is what
  // this machine reads, and `turn()` is the same object's other half, which
  // P3.5's screen hands to `interruptDecision`. A caller with a `speaking` flag
  // of its own has built the second source of truth D206 forbids.
  if (
    ctx.pipeline === null ||
    typeof ctx.pipeline !== 'object' ||
    typeof ctx.pipeline.speaking !== 'function' ||
    typeof ctx.pipeline.turn !== 'function'
  ) {
    throw new TypeError('ctx.pipeline is the pipeline: speaking() and turn() (D206)');
  }

  if (event === null || typeof event !== 'object' || Array.isArray(event)) {
    return refuse(MACHINE_CODES.UNKNOWN_EVENT, 'type');
  }
  // §5's kinds are judged by VS0.1's validator, so a malformed one is refused
  // here too rather than reduced. The validator answers yes or no; the native
  // port (VS1.2) refuses it first, and is the place that can name the field.
  if (SESSION_EVENTS.includes(event.type) && !isVoiceSessionEvent(event)) {
    return refuse(MACHINE_CODES.MALFORMED_EVENT, null);
  }
  if (!MACHINE_EVENTS.includes(event.type)) return refuse(MACHINE_CODES.UNKNOWN_EVENT, 'type');

  return HANDLERS[event.type](state, event, ctx);
}
