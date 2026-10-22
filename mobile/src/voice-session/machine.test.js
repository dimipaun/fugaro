// The session machine, walked against §4's table (voice-session plan, Task
// VS0.2; D205, D206, D201, P8, F3).
//
// **What this file is.** One reducer, and the table the spec writes: every
// state, every event, and the pairs with no transition. The plan's own list is
// asserted by name below -- `idle → starting → listening`, `listening ⇄
// speaking` driven by the pipeline, each active state through
// `paused_interruption` and back, the timeout, `shouldResume: false`, the route
// pause that resumes only on an explicit action, the hang-up, mute as an
// orthogonal flag, and the P8 caveat -- and the walk at the end covers the rest,
// so a state or an event added later cannot arrive untested.
//
// **Two doubles, and which is which.** Most of this file hands the machine a
// two-method pipeline double: the machine reads `speaking()` and nothing else,
// and a double is what makes that visible (one test below hands it a pipeline
// whose `turn()` throws, and the machine reduces fine -- `turn` is P3.5's, and
// judging the turn here would be the second source of truth D206 forbids). The
// `listening ⇄ speaking` test is the exception: it drives **P3.4's real
// pipeline** with its own fakes (`FakeAudioIn`/`FakeStt`/`FakeTts`), because
// the thing that test is about is that the machine and the pipeline agree on
// what "speaking" means.
//
// **The clock and the timeout are the caller's.** `now` is a closure over a
// number this file moves by hand, and `resumeTimeoutMs` is passed per call --
// §8's own 180,000 ms is used as the ordinary value, and D201 (120,000 ms) is
// still open, which is exactly why the machine has no default of its own.

import assert from 'node:assert/strict';
import test from 'node:test';

import { FakeAudioIn, FakeStt, FakeTts, createVoicePipeline } from '../audio/index.js';

import { END_REASONS, PAUSE_REASONS, SESSION_EVENTS, SESSION_STATES } from './events.js';
import { INITIAL_SESSION_STATE, MACHINE_CODES, MACHINE_EVENTS, reduce } from './machine.js';

/** §8's own figure. D201's 120,000 ms is a recommendation, not this machine's. */
const TIMEOUT = 180_000;

/**
 * A `ctx`, with a clock this file can move and a pipeline double that answers
 * `speaking()` with whatever the test set. `turn()` answers too, because the
 * machine requires it to be there (D206) -- it is never read here.
 */
function ctx({ now = 0, resumeTimeoutMs = TIMEOUT, pipeline = null, speaking = false } = {}) {
  return {
    now: typeof now === 'function' ? now : () => now,
    resumeTimeoutMs,
    pipeline: pipeline ?? { speaking: () => speaking, turn: () => false },
  };
}

/** A state to reduce into: the initial one, in `name`, with those fields. */
function state(name, changes = {}) {
  return Object.freeze({ ...INITIAL_SESSION_STATE, state: name, ...changes });
}

/** Reduce, asserting the verdict is a transition, and answer the new state. */
function moved(from, event, c = ctx()) {
  const verdict = reduce(from, event, c);
  assert.equal(verdict.ok, true, `${event.type} in ${from.state}: ${JSON.stringify(verdict)}`);
  return verdict.state;
}

/** Reduce, asserting the verdict is a refusal with that code. */
function refused(from, event, code, c = ctx()) {
  const verdict = reduce(from, event, c);
  assert.equal(verdict.ok, false, `${event.type} in ${from.state} was not refused`);
  assert.equal(verdict.code, code, `${event.type} in ${from.state}`);
  return verdict;
}

/** A well-formed event of each machine kind, with the ordinary field values. */
const EVENT = Object.freeze({
  start: { type: 'start' },
  started: { type: 'started' },
  hangUp: { type: 'hangUp' },
  end: { type: 'end' },
  resume: { type: 'resume' },
  tick: { type: 'tick' },
  interruptionBegan: { type: 'interruptionBegan', source: 'call' },
  interruptionEnded: { type: 'interruptionEnded', shouldResume: true },
  routeChanged: { type: 'routeChanged', route: 'wired', unsafe: true },
  remoteAction: { type: 'remoteAction', action: 'mute' },
  serviceStopped: { type: 'serviceStopped', reason: 'system' },
  error: { type: 'error', code: 'boom', message: 'the engine stopped' },
});

// ---------------------------------------------------------------------------
// The plan's list, one test at a time
// ---------------------------------------------------------------------------

test('idle → starting → listening: the app asks, the native port answers', () => {
  const started = moved(INITIAL_SESSION_STATE, EVENT.start, ctx({ now: 1_700_000_000_000 }));
  assert.equal(started.state, 'starting');
  assert.equal(started.startedAt, 1_700_000_000_000, 'the session remembers when it was asked for');
  assert.equal(moved(started, EVENT.started).state, 'listening');
  // The pipeline is the truth about speaking, so a session that comes up while
  // an utterance is in flight is `speaking`, not `listening` (D206).
  assert.equal(moved(started, EVENT.started, ctx({ speaking: true })).state, 'speaking');
});

test('listening ⇄ speaking is driven by P3.4\'s real pipeline, with no event in between', async () => {
  const audioIn = new FakeAudioIn();
  const stt = new FakeStt();
  const tts = new FakeTts();
  const pipeline = createVoicePipeline({ audioIn, stt, tts, sender: 'me', newId: () => 'id', now: () => 0 });
  const c = ctx({ pipeline });
  await pipeline.start();

  const listening = moved(moved(INITIAL_SESSION_STATE, EVENT.start, c), EVENT.started, c);
  assert.equal(listening.state, 'listening');

  // The user starts talking: the pipeline opens a turn. No event is reduced --
  // the state only moves because the machine re-asks the pipeline (D206).
  audioIn.speechStart();
  assert.equal(pipeline.turn(), true);
  assert.equal(moved(listening, EVENT.tick, c).state, 'listening', 'a turn is not speaking');

  // A reply is in flight: the same state, the same ctx, a different answer.
  const said = pipeline.say('the update is ready');
  assert.equal(pipeline.speaking(), true);
  const speaking = moved(listening, EVENT.tick, c);
  assert.equal(speaking.state, 'speaking');

  tts.finish();
  await said;
  assert.equal(moved(speaking, EVENT.tick, c).state, 'listening', 'the utterance ended, so it listens again');
});

test('the machine reads speaking() and never judges the turn', () => {
  // `turn` is P3.5's: the screen hands it to `interruptDecision`. A machine that
  // read it would be deciding what the user is doing, which is the pipeline's
  // and the screen's business -- so this pipeline throws if anything asks.
  const pipeline = {
    speaking: () => false,
    turn: () => assert.fail('the machine must not read the turn'),
  };
  const c = ctx({ pipeline });
  assert.equal(moved(state('listening'), EVENT.tick, c).state, 'listening');
  assert.equal(moved(state('starting'), EVENT.started, c).state, 'listening');
});

test('each active state → paused_interruption → listening on shouldResume: true, within the timeout', () => {
  for (const from of ['starting', 'listening', 'speaking']) {
    const c = ctx({ now: 1_000 });
    const paused = moved(state(from), EVENT.interruptionBegan, c);
    assert.equal(paused.state, 'paused_interruption', from);
    assert.equal(paused.pauseReason, PAUSE_REASONS.INTERRUPTION, from);
    assert.equal(paused.pauseSource, 'call', from);
    assert.equal(paused.pausedAt, 1_000, from);

    const back = moved(paused, EVENT.interruptionEnded, ctx({ now: 1_000 + TIMEOUT }));
    assert.equal(back.state, 'listening', `${from} resumed`);
    assert.equal(back.pauseReason, null, from);
    assert.equal(back.pauseSource, null, from);
    assert.equal(back.pausedAt, null, from);
  }
});

test('a tick past the timeout ends it as interruption_timeout, and the pause survives one inside it', () => {
  const c = ctx({ now: 0 });
  const paused = moved(state('listening'), EVENT.interruptionBegan, c);
  // §4's condition is *elapsed ≤ resumeTimeoutMs*: exactly the limit still
  // resumes, and the tick that fires is the first one strictly past it.
  assert.equal(moved(paused, EVENT.tick, ctx({ now: TIMEOUT })).state, 'paused_interruption');
  assert.equal(moved(paused, EVENT.tick, ctx({ now: TIMEOUT + 1 })).state, 'ended');
  assert.equal(moved(paused, EVENT.tick, ctx({ now: TIMEOUT + 1 })).endReason, END_REASONS.INTERRUPTION_TIMEOUT);
  // ... and an `interruptionEnded` that arrives late is not a resume either,
  // whether or not a tick was injected first (§4, §8).
  assert.equal(moved(paused, EVENT.interruptionEnded, ctx({ now: TIMEOUT + 1 })).state, 'ended');
  assert.equal(moved(paused, EVENT.interruptionEnded, ctx({ now: TIMEOUT })).state, 'listening');
});

test('shouldResume: false ends it, and with the reason §4 gives that path', () => {
  const paused = moved(state('listening'), EVENT.interruptionBegan, ctx({ now: 0 }));
  const over = moved(paused, { type: 'interruptionEnded', shouldResume: false }, ctx({ now: 0 }));
  assert.equal(over.state, 'ended');
  assert.equal(over.endReason, END_REASONS.INTERRUPTION_TIMEOUT);
});

test('a second interruption does not restart §8\'s timer', () => {
  const paused = moved(state('listening'), EVENT.interruptionBegan, ctx({ now: 0 }));
  const again = moved(paused, { type: 'interruptionBegan', source: 'system' }, ctx({ now: TIMEOUT }));
  assert.equal(again.state, 'paused_interruption');
  assert.equal(again.pauseSource, 'system', 'what took the audio is the newest event\'s');
  assert.equal(again.pausedAt, 0, 'the timer still runs from the first interruption');
  assert.equal(moved(again, EVENT.tick, ctx({ now: TIMEOUT + 1 })).state, 'ended');
});

test('an unsafe route pauses, and the pause resumes only on an explicit action', () => {
  for (const from of ['starting', 'listening', 'speaking', 'paused_interruption']) {
    const paused = moved(state(from), { type: 'routeChanged', route: 'wired', unsafe: true });
    assert.equal(paused.state, 'paused_route', from);
    assert.equal(paused.pauseReason, PAUSE_REASONS.ROUTE, from);
    assert.equal(paused.route, 'wired', from);
    assert.equal(paused.pausedAt, null, from, 'a route pause has no timer to run out');

    // No time, and no event on the user's behalf, resumes it: not a tick (even
    // far past the interruption timeout), not a safe route, not a second one.
    assert.equal(moved(paused, EVENT.tick, ctx({ now: 10 * TIMEOUT })).state, 'paused_route', from);
    assert.equal(
      moved(paused, { type: 'routeChanged', route: 'speaker', unsafe: false }).state,
      'paused_route',
      `${from}: a safe route is recorded, not resumed onto`,
    );
    assert.equal(moved(paused, EVENT.resume).state, 'listening', `${from} resumed on the explicit action`);
  }
  // A safe route change is not a pause at all (§10's connect): the session
  // carries on, and remembers where the audio is.
  const carried = moved(state('listening'), { type: 'routeChanged', route: 'bluetooth', unsafe: false });
  assert.equal(carried.state, 'listening');
  assert.equal(carried.route, 'bluetooth');
});

test('a route that went unsafe during an interruption outranks it', () => {
  // §4 has one pause that resumes on its own and one that waits for a person.
  // A call ending must not resume onto a route the user has not accepted, so
  // the pause becomes the route's and the interruption can no longer end it.
  const paused = moved(state('listening'), EVENT.interruptionBegan, ctx({ now: 0 }));
  const route = moved(paused, { type: 'routeChanged', route: 'unknown', unsafe: true });
  assert.equal(route.state, 'paused_route');
  assert.equal(route.pausedAt, null);
  assert.equal(refused(route, EVENT.interruptionEnded, MACHINE_CODES.NO_TRANSITION).field, 'interruptionEnded');
  assert.equal(moved(route, EVENT.tick, ctx({ now: TIMEOUT + 1 })).state, 'paused_route');
});

test('hangUp from every active state → ending → ended', () => {
  for (const from of ['starting', 'listening', 'speaking', 'paused_interruption', 'paused_route']) {
    const changes = from === 'paused_interruption' ? { pausedAt: 0 } : {};
    const ending = moved(state(from, changes), EVENT.hangUp);
    assert.equal(ending.state, 'ending', from);
    assert.equal(ending.endReason, END_REASONS.HANGUP, from);
    assert.equal(moved(ending, EVENT.end).state, 'ended', from);
    // §4: the Live Activity's and the notification's hang-up is the same rule.
    assert.equal(moved(state(from, changes), { type: 'remoteAction', action: 'hangUp' }).state, 'ending', from);
  }
  // There is no session to hang up, and a second hang-up is the same hang-up.
  assert.equal(refused(INITIAL_SESSION_STATE, EVENT.hangUp, MACHINE_CODES.NO_TRANSITION).field, 'hangUp');
  const ending = moved(state('listening'), EVENT.hangUp);
  assert.equal(moved(ending, EVENT.hangUp).state, 'ending');
  assert.equal(moved(moved(ending, EVENT.end), EVENT.hangUp).state, 'ended');
});

test('a session that ends carries no pause: the pause fields are "while paused"', () => {
  // `pauseReason`/`pauseSource`/`pausedAt` answer "why is it paused *now*" --
  // §4 has exactly two paused states and `ending`/`ended` are not among them.
  // A teardown that kept the pause it left would have the ended session answer
  // that question with a call that is over, and a screen or a Live Activity
  // reading the fields would say "Paused — on a call" about a session that has
  // none.
  const noPause = (s, where) => {
    assert.equal(s.pauseReason, null, `${where}: pauseReason`);
    assert.equal(s.pauseSource, null, `${where}: pauseSource`);
    assert.equal(s.pausedAt, null, `${where}: pausedAt`);
  };
  // Hang up from either pause: the teardown and the end both drop it.
  for (const [from, changes] of [
    ['paused_interruption', { pausedAt: 0, pauseReason: PAUSE_REASONS.INTERRUPTION, pauseSource: 'call' }],
    ['paused_route', { pauseReason: PAUSE_REASONS.ROUTE, route: 'wired' }],
  ]) {
    const ending = moved(state(from, changes), EVENT.hangUp);
    assert.equal(ending.state, 'ending', from);
    noPause(ending, `${from} → ending`);
    noPause(moved(ending, EVENT.end), `${from} → ended`);
  }
  // ... and every other way a paused session ends: §8's timeout, §4's own
  // `shouldResume: false`, §10's revoked permission, and §9's service stop.
  const paused = () => moved(state('listening'), EVENT.interruptionBegan, ctx({ now: 0 }));
  noPause(moved(paused(), EVENT.tick, ctx({ now: TIMEOUT + 1 })), 'the timeout');
  noPause(moved(paused(), { type: 'interruptionEnded', shouldResume: false }, ctx({ now: 0 })), 'shouldResume: false');
  noPause(moved(paused(), EVENT.error, ctx({ now: 0 })), 'error');
  noPause(moved(paused(), EVENT.serviceStopped, ctx({ now: 0 })), 'serviceStopped');
  const route = moved(state('listening'), { type: 'routeChanged', route: 'wired', unsafe: true });
  noPause(moved(route, EVENT.serviceStopped), 'a route pause, ended');
  // The positive control: the same fields *are* set while the session is
  // paused, so this test cannot pass by the machine never setting them.
  assert.equal(paused().pauseReason, PAUSE_REASONS.INTERRUPTION);
  assert.equal(paused().pauseSource, 'call');
  assert.equal(paused().pausedAt, 0);
});

test('mute is orthogonal: it changes no state, and it applies on resume (§10)', () => {
  const active = {
    starting: {},
    listening: {},
    speaking: {},
    paused_interruption: { pausedAt: 0, pauseReason: PAUSE_REASONS.INTERRUPTION, pauseSource: 'call' },
    paused_route: { pauseReason: PAUSE_REASONS.ROUTE, route: 'wired' },
    ending: { endReason: END_REASONS.HANGUP },
  };
  for (const [name, changes] of Object.entries(active)) {
    const before = state(name, changes);
    const muted = moved(before, EVENT.remoteAction);
    assert.equal(muted.state, name, name);
    assert.equal(muted.muted, true, name);
    assert.deepEqual({ ...muted, muted: false }, before, `${name}: nothing but the flag moved`);
    assert.equal(moved(muted, { type: 'remoteAction', action: 'unmute' }).muted, false, name);
  }
  // Pressed while the session is paused, it survives the resume: the flag is
  // the session's, not the pause's.
  const muted = moved(state('listening'), EVENT.remoteAction);
  const paused = moved(muted, EVENT.interruptionBegan, ctx({ now: 0 }));
  assert.equal(moved(paused, EVENT.interruptionEnded, ctx({ now: 0 })).muted, true);
});

test('P8: an interruption that was really a suspension is not an interruption', () => {
  // iOS sends `AVAudioSessionInterruptionWasSuspendedKey` when it resumes a
  // suspended app. Acting on it would report a call that never happened -- so
  // in every state, it changes nothing at all.
  for (const name of SESSION_STATES) {
    const before = state(name, name === 'paused_interruption' ? { pausedAt: 0 } : {});
    const verdict = reduce(before, { type: 'interruptionBegan', source: 'system', suspended: true }, ctx({ now: 5 }));
    assert.equal(verdict.ok, true, `${name} accepted the suspension notification`);
    assert.deepEqual(verdict.state, before, `${name} is unchanged by a suspension`);
  }
  // The positive control: the same event without the key is an interruption.
  assert.equal(
    moved(state('listening'), { type: 'interruptionBegan', source: 'system', suspended: false }, ctx({ now: 5 })).state,
    'paused_interruption',
  );
});

test('the first end reason is kept', () => {
  // A hang-up stops the Android service, which reports itself. "Why did this
  // end" is answered with the cause, not the consequence.
  const ending = moved(state('listening'), EVENT.hangUp);
  assert.equal(moved(ending, EVENT.serviceStopped).endReason, END_REASONS.HANGUP);
  // ... and an error arriving after the end is not news either.
  const ended = moved(moved(ending, EVENT.end), EVENT.error);
  assert.equal(ended.endReason, END_REASONS.HANGUP);
  assert.equal(ended.error, null);
});

test('error ends the session, and §10\'s revoked permission is its own reason', () => {
  const revoked = moved(state('listening'), { type: 'error', code: 'permission', message: 'the microphone was revoked' });
  assert.equal(revoked.state, 'ended');
  assert.equal(revoked.endReason, END_REASONS.PERMISSION);
  assert.deepEqual(revoked.error, { code: 'permission', message: 'the microphone was revoked' }, 'D213: what failed is kept');
  assert.equal(moved(state('listening'), EVENT.error).endReason, END_REASONS.ERROR);
  // Before a session there is nothing to end, and the error is still worth
  // showing: the screen is what reports it.
  const idle = moved(INITIAL_SESSION_STATE, EVENT.error);
  assert.equal(idle.state, 'idle');
  assert.equal(idle.error.code, 'boom');
});

test('the platform stopping the service ends the session, and the launch cleanup is not a session', () => {
  for (const from of ['starting', 'listening', 'speaking', 'paused_interruption', 'paused_route', 'ending']) {
    const ended = moved(state(from, from === 'paused_interruption' ? { pausedAt: 0 } : {}), EVENT.serviceStopped);
    assert.equal(ended.state, 'ended', from);
  }
  // §9's other half is VS5.1's orphan service: with no session there is nothing
  // to end, and inventing an `ended` here would invent a session that never ran.
  assert.deepEqual(moved(INITIAL_SESSION_STATE, EVENT.serviceStopped), INITIAL_SESSION_STATE);
});

// ---------------------------------------------------------------------------
// The two refusals a caller keeps its state for
// ---------------------------------------------------------------------------

test('an unknown event is refused and changes nothing', () => {
  // A platform's own event, arriving from a native port that was changed on one
  // side: not a session event, and not reduced as one.
  for (const event of [
    { type: 'audioFocusLost' },
    { type: 'interruptionbegan', source: 'call' },
    { type: 'unavailable' }, // one of the pipeline's own events, reduced nowhere here
    { type: '' },
    {},
    // A `type` that names an `Object.prototype` member: the contract's own map
    // is a plain object, so this is where a lookup that is not `Object.hasOwn`
    // would throw instead of refusing.
    { type: 'constructor' },
    { type: 'toString' },
    'tick',
    7,
    [],
    null,
  ]) {
    const before = state('listening');
    const verdict = reduce(before, event, ctx());
    assert.equal(verdict.ok, false, JSON.stringify(event));
    assert.equal(verdict.code, MACHINE_CODES.UNKNOWN_EVENT, JSON.stringify(event));
    assert.deepEqual(before, state('listening'), 'the state the caller holds is untouched');
  }
});

test('a malformed §5 event is refused by VS0.1\'s validator, not guessed at', () => {
  for (const event of [
    { type: 'interruptionEnded' }, // §6.2's field, and the one an emitter forgets
    { type: 'interruptionEnded', shouldResume: 'yes' },
    { type: 'routeChanged', route: 'headphones', unsafe: false }, // a platform's own route name
    { type: 'routeChanged', route: 'wired' }, // unsafe is the whole question §6.3 asks
    { type: 'interruptionBegan', source: 'alarm' },
    { type: 'remoteAction', action: 'resume' },
    { type: 'error', code: '', message: 'x' },
    { type: 'interruptionEnded', shouldResume: true, source: 'call' }, // a field this kind does not carry
  ]) {
    const verdict = reduce(state('listening'), event, ctx());
    assert.equal(verdict.ok, false, JSON.stringify(event));
    assert.equal(verdict.code, MACHINE_CODES.MALFORMED_EVENT, JSON.stringify(event));
  }
});

// ---------------------------------------------------------------------------
// The machine is a function: no state it was handed changes, and a wiring bug
// is a throw rather than a phone's event
// ---------------------------------------------------------------------------

test('every verdict is frozen, and no call changes the state it was handed', () => {
  for (const name of SESSION_STATES) {
    const before = state(name, name === 'paused_interruption' ? { pausedAt: 0 } : {});
    const copy = JSON.parse(JSON.stringify(before));
    for (const type of MACHINE_EVENTS) {
      const verdict = reduce(before, EVENT[type], ctx({ now: 1 }));
      assert.equal(Object.isFrozen(verdict), true, `${type} in ${name}`);
      if (verdict.ok) assert.equal(Object.isFrozen(verdict.state), true, `${type} in ${name}`);
      assert.deepEqual(before, copy, `${type} in ${name} changed the state it was handed`);
      assert.equal(SESSION_STATES.includes(verdict.ok ? verdict.state.state : name), true);
    }
  }
  // Two sessions that started from the initial state cannot affect each other:
  // the constant is frozen, and every answer is a new object.
  assert.throws(() => {
    INITIAL_SESSION_STATE.state = 'listening';
  }, TypeError);
});

test('a state, a ctx or a pipeline the machine could not have been handed throws', () => {
  const c = ctx();
  for (const bad of [null, undefined, 'listening', {}, { state: 'paused' }, []]) {
    assert.throws(() => reduce(bad, EVENT.tick, c), TypeError, String(bad));
  }
  assert.throws(() => reduce(state('listening'), EVENT.tick, null), TypeError);
  assert.throws(() => reduce(state('listening'), EVENT.tick, {}), TypeError);
  assert.throws(() => reduce(state('listening'), EVENT.tick, { ...c, now: 0 }), TypeError);
  assert.throws(() => reduce(state('listening'), EVENT.tick, { ...c, resumeTimeoutMs: '180000' }), TypeError);
  assert.throws(() => reduce(state('listening'), EVENT.tick, { ...c, resumeTimeoutMs: -1 }), TypeError);
  assert.throws(() => reduce(state('listening'), EVENT.tick, { ...c, resumeTimeoutMs: NaN }), TypeError);
  // A caller with a `speaking` flag of its own has built the second source of
  // truth D206 forbids, so it is refused at the call rather than at the phone.
  for (const pipeline of [null, {}, { speaking: () => false }, { speaking: () => false, turn: true }]) {
    assert.throws(() => reduce(state('listening'), EVENT.tick, { ...c, pipeline }), TypeError, JSON.stringify(pipeline));
  }
});

// ---------------------------------------------------------------------------
// The walk: every state against every event, so a pair added later is a
// deliberate edit to a literal here
// ---------------------------------------------------------------------------

/** Every state, with the fields that state really carries. */
const WALK_STATES = Object.freeze({
  idle: {},
  starting: { startedAt: 0 },
  listening: { startedAt: 0 },
  speaking: { startedAt: 0 },
  paused_interruption: { startedAt: 0, pauseReason: PAUSE_REASONS.INTERRUPTION, pauseSource: 'call', pausedAt: 0 },
  paused_route: { startedAt: 0, pauseReason: PAUSE_REASONS.ROUTE, route: 'wired' },
  ending: { startedAt: 0, endReason: END_REASONS.HANGUP },
  ended: { startedAt: 0, endReason: END_REASONS.HANGUP },
});

/** Where each state goes, for the events §4's table gives it an entry for. */
const WALK = Object.freeze({
  idle: { start: 'starting', tick: 'idle', serviceStopped: 'idle', error: 'idle' },
  starting: {
    started: 'listening',
    hangUp: 'ending',
    tick: 'starting',
    interruptionBegan: 'paused_interruption',
    routeChanged: 'paused_route',
    remoteAction: 'starting',
    serviceStopped: 'ended',
    error: 'ended',
  },
  listening: {
    hangUp: 'ending',
    tick: 'listening',
    interruptionBegan: 'paused_interruption',
    routeChanged: 'paused_route',
    remoteAction: 'listening',
    serviceStopped: 'ended',
    error: 'ended',
  },
  speaking: {
    hangUp: 'ending',
    // The pipeline is the truth (D206): this ctx's double is not speaking, so a
    // `speaking` state that is asked again is `listening`.
    tick: 'listening',
    interruptionBegan: 'paused_interruption',
    routeChanged: 'paused_route',
    remoteAction: 'speaking',
    serviceStopped: 'ended',
    error: 'ended',
  },
  paused_interruption: {
    hangUp: 'ending',
    tick: 'paused_interruption',
    interruptionBegan: 'paused_interruption',
    interruptionEnded: 'listening',
    routeChanged: 'paused_route',
    remoteAction: 'paused_interruption',
    serviceStopped: 'ended',
    error: 'ended',
  },
  paused_route: {
    hangUp: 'ending',
    resume: 'listening',
    tick: 'paused_route',
    routeChanged: 'paused_route',
    remoteAction: 'paused_route',
    serviceStopped: 'ended',
    error: 'ended',
  },
  ending: {
    hangUp: 'ending',
    end: 'ended',
    tick: 'ending',
    // The flag is orthogonal to the state, and a teardown still has a session:
    // a mute that arrives while it is ending is recorded like any other (§4
    // gives no rule that refuses one, and `idle`/`ended` are where the flag has
    // nothing to belong to).
    remoteAction: 'ending',
    serviceStopped: 'ended',
    error: 'ended',
  },
  ended: {
    start: 'starting',
    hangUp: 'ended',
    tick: 'ended',
    serviceStopped: 'ended',
    error: 'ended',
  },
});

/** The pairs §4's table gives no entry for, which is what a refusal means. */
const NO_ENTRY = Object.freeze({
  idle: ['started', 'hangUp', 'end', 'resume', 'interruptionBegan', 'interruptionEnded', 'routeChanged', 'remoteAction'],
  starting: ['start', 'end', 'resume', 'interruptionEnded'],
  listening: ['start', 'started', 'end', 'resume', 'interruptionEnded'],
  speaking: ['start', 'started', 'end', 'resume', 'interruptionEnded'],
  paused_interruption: ['start', 'started', 'end', 'resume'],
  paused_route: ['start', 'started', 'end', 'interruptionBegan', 'interruptionEnded'],
  ending: ['start', 'started', 'resume', 'interruptionBegan', 'interruptionEnded', 'routeChanged'],
  ended: ['started', 'end', 'resume', 'interruptionBegan', 'interruptionEnded', 'routeChanged', 'remoteAction'],
});

test('the walk: every state, every event, and the pairs with no transition', () => {
  for (const [name, changes] of Object.entries(WALK_STATES)) {
    const before = state(name, changes);
    const walked = new Set();
    for (const [type, expected] of Object.entries(WALK[name])) {
      assert.equal(moved(before, EVENT[type], ctx({ now: 0 })).state, expected, `${type} in ${name}`);
      walked.add(type);
    }
    for (const type of NO_ENTRY[name]) {
      refused(before, EVENT[type], MACHINE_CODES.NO_TRANSITION, ctx({ now: 0 }));
      walked.add(type);
    }
    // No state is walked for some of the events only: a kind added to the
    // machine later fails here until this table says what it does.
    assert.deepEqual([...walked].sort(), [...MACHINE_EVENTS].sort(), `${name} is walked for every event`);
    // §5's six kinds are all reduced by this machine -- the validator accepts
    // none that the table then refuses as unknown.
    for (const kind of SESSION_EVENTS) assert.equal(walked.has(kind), true, kind);
    assert.deepEqual(before, state(name, changes), `${name} was changed by the walk`);
  }
});
