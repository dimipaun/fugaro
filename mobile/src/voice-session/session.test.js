// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The session controller's rules (voice-session plan, Task VS1.1; D206, D217,
// F3, F4, F24, D201).
//
// The plan's verification is six sentences, and the first six tests below are
// those six, in its order: *start drives both in the right order; a state change
// pushes exactly one `updateStatus` and a mute change pushes exactly one more; a
// render with no change pushes none; `interruptDecision` sees the pipeline's
// live `speaking`/`turn`; a paused session issues no mailbox call and reads no
// credential; the machine's `ended` releases the lease exactly once.*
//
// **What is real here.** VS0.2's machine, P3.4's pipeline over its three fakes,
// and P3.5's screen: the session is the only new object, and it is driven
// through the events the native port will deliver (VS1.2). Two doubles stand in
// for the two things this task does not have -- §5's `VoiceSessionNative`, which
// is VS1.2's file, and P3.6's client, which this module may not import at all
// (F24) and which is therefore a two-line object here rather than the real one.
// Nothing below reads a clock, a device or a network: `now` is a variable a test
// moves (Global Constraint 10).

import assert from 'node:assert/strict';
import test from 'node:test';

import { SEVERITY } from '../../../shared/digest/index.js';
import { emptyLeaseState } from '../../../shared/lease/index.js';
import { FakeAudioIn, FakeStt, FakeTts, createVoicePipeline } from '../audio/index.js';
import { INTERRUPT_SWITCH, createVoiceScreen } from '../voice/index.js';
import { INTERRUPTION_SOURCES, REMOTE_ACTIONS } from './events.js';
import * as machine from './machine.js';
import { SESSION_CODES, createVoiceSession } from './session.js';

const T0 = 1_700_000_000_000;
/**
 * §8's window, handed in. D201 -- the *default* -- is the owner's decision and
 * still open, so the session is given a number and never invents one.
 */
const RESUME_MS = 180_000;
const TITLE = 'fugaro session';
/** P1.2's holder: the phone's own name for itself, and never the laptop's. */
const PHONE = 'phone-installation-a';
/** D114's writing identity, in the shape the shared schema accepts. */
const UID = 'AbCdEf0123456789ghIJkl';

/**
 * Let the app's promise chains drain before a test looks at what they did.
 *
 * `setImmediate` and not a timer: it schedules no delay, it only yields until
 * the microtask queue is empty. No file under `src/voice-session/` that ships
 * has one of these -- the module's scan forbids a timer in production code.
 */
const settle = () => new Promise((resolve) => setImmediate(resolve));

/**
 * §5's `VoiceSessionNative`, recorded: `start`, `updateStatus`, `setMuted` and
 * `end`, which is the whole of it that this module calls. Every call is
 * recorded with the microphone's state at the moment it was made, because that
 * is how the teardown's order -- close the microphone, then end the port -- is
 * assertable rather than asserted.
 *
 * @param {object} [options]
 * @param {boolean} [options.refuseStart] make `start` reject: Android 14's
 *   background restriction, and the path where nothing ever began
 * @param {boolean} [options.refuseEnd] make `end` reject
 * @param {() => boolean} [options.microphoneOpen] what each call records
 */
function nativeDouble({ refuseStart = false, refuseEnd = false, microphoneOpen = null } = {}) {
  const calls = [];
  const record = (name, args, verdict) => {
    calls.push(
      Object.freeze({
        name,
        args,
        open: microphoneOpen === null ? null : microphoneOpen(),
      }),
    );
    return verdict();
  };
  return {
    calls,
    start(options) {
      return record('start', [options], () => (refuseStart ? Promise.reject(new Error('no')) : Promise.resolve()));
    },
    updateStatus(status) {
      return record('updateStatus', [status], () => Promise.resolve());
    },
    setMuted(muted) {
      return record('setMuted', [muted], () => Promise.resolve());
    },
    end(reason) {
      return record('end', [reason], () => (refuseEnd ? Promise.reject(new Error('no')) : Promise.resolve()));
    },
  };
}

/** The calls of one kind, in order, with the microphone's state beside each. */
const callsNamed = (native, name) => native.calls.filter((call) => call.name === name);

/** The `updateStatus` payloads, in order. */
const statuses = (native) => callsNamed(native, 'updateStatus').map((call) => call.args[0]);

/** P3.6's client, recorded: a port this module may reach only through P3.5. */
function clientDouble() {
  const calls = [];
  const record = (name) => (text) => {
    calls.push(Object.freeze({ name, text }));
    return Promise.resolve({ ok: true });
  };
  return { calls, send: record('send'), poll: record('poll'), licenceRefusal: record('licenceRefusal') };
}

/**
 * The client F24 is about: the phone is locked, so the secure store cannot be
 * read, so **every** call throws -- and each one is recorded first, so "no
 * mailbox call was made" is an assertion and not only a hope.
 */
function lockedClient() {
  const calls = [];
  const record = (name) => () => {
    calls.push(Object.freeze({ name }));
    throw new Error(`the store is unreadable while the phone is locked (${name})`);
  };
  return { calls, send: record('send'), poll: record('poll'), licenceRefusal: record('licenceRefusal') };
}

/**
 * The whole session, over the real machine, the real pipeline and the real
 * screen, with the device replaced. `now` is a variable rather than a clock.
 *
 * The screen is handed on wrapped in one counted method: P1.2's `releaseLease`
 * on a lease that is already back is a no-op, so the *count* is the only way
 * "the lease goes back exactly once" can be seen.
 */
function harness({ refuseStart = false, refuseEnd = false, client = null } = {}) {
  const audioIn = new FakeAudioIn();
  const stt = new FakeStt();
  const tts = new FakeTts();
  let nowMs = T0;
  let ids = 0;
  const pipeline = createVoicePipeline({
    audioIn,
    stt,
    tts,
    sender: UID,
    newId: () => `m-${++ids}`,
    now: () => nowMs,
  });
  const mailbox = client ?? clientDouble();
  const built = createVoiceScreen({
    pipeline,
    client: mailbox,
    now: () => nowMs,
    holder: PHONE,
    switch: INTERRUPT_SWITCH.ON,
  });
  let releases = 0;
  const screen = {
    ...built,
    releaseLease: () => {
      releases += 1;
      return built.releaseLease();
    },
  };
  const native = nativeDouble({ refuseStart, refuseEnd, microphoneOpen: () => audioIn.running() });
  const session = createVoiceSession({
    machine,
    screen,
    pipeline,
    native,
    now: () => nowMs,
    resumeTimeoutMs: RESUME_MS,
    title: TITLE,
  });
  return {
    session,
    screen,
    pipeline,
    native,
    mailbox,
    audioIn,
    stt,
    tts,
    releases: () => releases,
    advance: (ms) => {
      nowMs += ms;
    },
  };
}

test('start drives the port and then the screen, and nothing before the port answered', async () => {
  const h = harness();
  const verdict = await h.session.start();

  assert.equal(verdict.ok, true);
  assert.equal(h.session.state().state, 'listening');
  assert.equal(h.audioIn.running(), true, 'screen.start() is the one path to the pipeline');
  // The port first: the service and the Live Activity exist before anything
  // reaches for the microphone. Then exactly one push per state change -- the
  // `starting` one *after* the port answered, because §5's payload carries the
  // `startedAt` a port that has not begun has nothing to render.
  assert.deepEqual(
    h.native.calls.map((call) => call.name),
    ['start', 'updateStatus', 'updateStatus'],
  );
  assert.deepEqual(h.native.calls[0].args, [{ title: TITLE, resumeTimeoutMs: RESUME_MS }]);
  assert.deepEqual(statuses(h.native), [
    { state: 'starting', muted: false, startedAt: T0 },
    { state: 'listening', muted: false, startedAt: T0 },
  ]);

  // A second start on a live session is the machine's refusal, and it reaches
  // neither the port nor the microphone.
  const again = await h.session.start();
  assert.deepEqual(again, { ok: false, code: 'no-transition', field: 'start' });
  assert.equal(h.audioIn.calls().starts, 1);
  assert.equal(callsNamed(h.native, 'start').length, 1);
});

test('a state change pushes one updateStatus, a mute change one more, and a render none', async () => {
  const h = harness();
  await h.session.start();
  assert.equal(statuses(h.native).length, 2);

  // A render with nothing changed pushes nothing: that throttle is D217's, and
  // it is the whole reason `updateStatus` is not called per frame.
  await h.session.tick();
  assert.equal(statuses(h.native).length, 2);

  // A mute change is the second thing D217 pushes on, and it changes no state.
  await h.session.setMuted(true);
  assert.equal(statuses(h.native).length, 3);
  assert.deepEqual(callsNamed(h.native, 'setMuted').map((call) => call.args[0]), [true]);
  assert.deepEqual(statuses(h.native).at(-1), { state: 'listening', muted: true, startedAt: T0 });
  assert.equal(h.session.state().muted, true);

  // ... and a tick with the flag already set is not a change either.
  await h.session.tick();
  assert.equal(statuses(h.native).length, 3);

  // A remote mute -- the Live Activity's button -- is the same change, and the
  // port is not asked to mute the audio a second time: it is already muted.
  await h.session.dispatch({ type: 'remoteAction', action: REMOTE_ACTIONS.UNMUTE });
  assert.equal(statuses(h.native).length, 4);
  assert.deepEqual(statuses(h.native).at(-1), { state: 'listening', muted: false, startedAt: T0 });
  assert.deepEqual(callsNamed(h.native, 'setMuted').map((call) => call.args[0]), [true]);
});

test("the interrupt decision is handed the pipeline's live pair, and no event is in between", async () => {
  const h = harness();
  await h.session.start();
  h.screen.setDigest({ severity: SEVERITY.kill });
  const update = { text: 'the nightly build failed', severity: SEVERITY.kill };

  // Nothing is in flight, so the boundary allows the update and it is spoken.
  h.screen.offer({ ...update });
  assert.equal(h.screen.queued().length, 0);
  assert.deepEqual(h.tts.spoken(), [update.text]);
  h.tts.finish();
  await settle();

  // The user takes the turn. **No session event is dispatched**: the pair moves
  // on its own, and the machine's own state does not move at all.
  h.audioIn.speechStart();
  assert.equal(h.session.state().screen.turn, true, 'the pair a render reads follows the pipeline');
  assert.equal(h.session.state().screen.speaking, false);
  assert.equal(h.session.state().state, 'listening', 'the machine has not moved; the pair has');

  // ... and the decision made from that pair queues the update rather than
  // speaking it over the user. A stored copy of the pair would have said the
  // opposite, because nothing has told this session anything since.
  h.screen.offer({ ...update });
  assert.equal(h.screen.queued().length, 1);
  assert.deepEqual(h.tts.spoken(), [update.text]);

  // The turn closes, and the same update goes out at the boundary it was
  // waiting for.
  h.audioIn.speechEnd();
  h.stt.final('what is the build doing');
  assert.equal(h.session.state().screen.turn, false);
  assert.equal(h.screen.queued().length, 0);
  assert.deepEqual(h.tts.spoken(), [update.text, update.text]);
  assert.deepEqual(h.mailbox.calls.map((call) => call.name), ['licenceRefusal', 'send']);
});

test('the session holds the machine’s fields and the screen’s snapshot, and no copy of the pair', async () => {
  const h = harness();
  await h.session.start();
  // F3/D206: `speaking` and `turn` are the pipeline's, read where they are
  // rendered. They are not fields of this object's own state, so there is
  // nothing here that can go stale.
  assert.deepEqual(Object.keys(h.session.state()).sort(), [
    'endReason',
    'error',
    'muted',
    'pauseReason',
    'pauseSource',
    'pausedAt',
    'route',
    'screen',
    'startedAt',
    'state',
  ]);
  assert.equal(h.session.state().screen.speaking, h.pipeline.speaking());
  assert.equal(h.session.state().screen.turn, h.pipeline.turn());
});

test('a paused session makes no mailbox call and reads no credential (F24)', async () => {
  // The phone is locked: the secure store is unreadable, so every call the
  // screen could make to its client throws. A session that made one -- on the
  // resume path, which is the call this module exists for -- fails here rather
  // than passing quietly. The import fence itself is `index.test.js`'s.
  const h = harness({ client: lockedClient() });
  await h.session.start();

  await h.session.dispatch({ type: 'interruptionBegan', source: INTERRUPTION_SOURCES.CALL });
  assert.equal(h.session.state().state, 'paused_interruption');
  assert.equal(h.session.state().pauseReason, 'interruption');

  // §10: a mute pressed during the interruption updates the flag, and it is a
  // push to the port -- never a mailbox call.
  await h.session.setMuted(true);
  assert.equal(h.session.state().muted, true);

  h.advance(60_000);
  await h.session.tick();
  assert.equal(h.session.state().state, 'paused_interruption', "inside §8's window: still paused");

  await h.session.dispatch({ type: 'interruptionEnded', shouldResume: true });
  assert.equal(h.session.state().state, 'listening');
  assert.equal(h.session.state().pauseReason, null);

  assert.deepEqual(h.mailbox.calls, [], 'nothing reached the store while the phone was locked');
});

test("the machine's `ended` gives the lease back, exactly once", async () => {
  const h = harness();
  // The lease, taken the way the app takes it: P3.5's own `takeLease`, with the
  // phone as the holder.
  h.screen.setLease(emptyLeaseState());
  assert.equal(h.screen.takeLease().ok, true);
  assert.equal(h.screen.lease().lease.holder, PHONE);

  await h.session.start();
  await h.session.hangUp();

  assert.equal(h.session.state().state, 'ended');
  assert.equal(h.session.state().endReason, 'hangup');
  assert.equal(h.releases(), 1);
  assert.equal(h.screen.lease().lease, null, 'the claim is back');

  // A second hang-up is the same hang-up (the machine's own rule), and it must
  // not give a lease back twice.
  await h.session.hangUp();
  assert.equal(h.releases(), 1);
});

test('a hang-up closes the microphone, then ends the port, and only then is the session over', async () => {
  const h = harness();
  await h.session.start();

  const verdict = await h.session.hangUp();
  assert.equal(verdict.ok, true);
  assert.equal(h.session.state().state, 'ended');
  // The port's last call is the one that ends it, and the microphone is already
  // closed when it arrives. `ending` is not pushed either: `end(reason)` is the
  // port's own copy of "the session is over", with the cause on it.
  assert.deepEqual(
    h.native.calls.map((call) => call.name),
    ['start', 'updateStatus', 'updateStatus', 'end'],
  );
  assert.deepEqual(h.native.calls.at(-1).args, ['hangup']);
  assert.equal(h.native.calls.at(-1).open, false, 'the microphone closes before the port ends');
  assert.equal(h.audioIn.running(), false);
});

test('§8’s timeout ends the session and the port, with no hang-up', async () => {
  const h = harness();
  await h.session.start();
  await h.session.dispatch({ type: 'interruptionBegan', source: INTERRUPTION_SOURCES.CALL });

  // §8's step 2, reached by time alone: the window passes, and the session ends
  // itself -- so the microphone closes and the port ends, exactly as a hang-up
  // does.
  h.advance(RESUME_MS + 1);
  await h.session.tick();

  assert.equal(h.session.state().state, 'ended');
  assert.equal(h.session.state().endReason, 'interruption_timeout');
  assert.deepEqual(callsNamed(h.native, 'end').map((call) => call.args[0]), ['interruption_timeout']);
  assert.equal(callsNamed(h.native, 'end').at(-1).open, false);
  assert.deepEqual(
    statuses(h.native).map((status) => status.state),
    ['starting', 'listening'],
    'the port is never told about a state it is about to be ended from',
  );
});

test('a port that will not begin ends the session, and nothing is torn down that never started', async () => {
  const h = harness({ refuseStart: true });
  const verdict = await h.session.start();

  assert.deepEqual(verdict, {
    ok: false,
    code: SESSION_CODES.START_FAILED,
    field: null,
  });
  assert.equal(h.session.state().state, 'ended');
  assert.equal(h.session.state().endReason, 'error');
  assert.deepEqual(h.session.state().error, {
    code: SESSION_CODES.START_FAILED,
    message: 'the voice session could not start',
  });
  assert.deepEqual(h.native.calls.map((call) => call.name), ['start'], 'a port that never began is not asked to end');
  assert.equal(h.audioIn.calls().starts, 0, 'nothing reached for the microphone');
  assert.equal(h.audioIn.calls().stops, 0);
});

test('a port that will not end is reported, and the session still ends', async () => {
  const h = harness({ refuseEnd: true });
  await h.session.start();
  await h.session.hangUp();

  // The cause is kept and the failure is on top of it: "why did this end" is
  // still the hang-up, and the teardown that did not finish is what the screen
  // shows beside it.
  assert.equal(h.session.state().state, 'ended');
  assert.equal(h.session.state().endReason, 'hangup');
  assert.deepEqual(h.session.state().error, {
    code: SESSION_CODES.END_FAILED,
    message: 'the voice session could not tear down',
  });
});

test('a session that has ended starts again, and the port is asked a second time', async () => {
  const h = harness();
  await h.session.start();
  await h.session.hangUp();

  // §8's "Tap to resume" starts a fresh session; this object carries it, and
  // the bookkeeping of the last one is not in its way.
  const again = await h.session.start();
  assert.equal(again.ok, true);
  assert.equal(h.session.state().state, 'listening');
  assert.equal(h.session.state().endReason, null);
  assert.deepEqual(
    statuses(h.native).map((status) => status.state),
    ['starting', 'listening', 'starting', 'listening'],
  );

  await h.session.hangUp();
  assert.equal(h.session.state().state, 'ended');
  assert.deepEqual(callsNamed(h.native, 'end').map((call) => call.args[0]), ['hangup', 'hangup']);
});

test('an event the outside world does not bring is refused, and changes nothing', async () => {
  const h = harness();
  await h.session.start();

  // `start`, `started` and `end` are this module's own: a caller that dispatched
  // one would move the machine without the port, which is the state this module
  // exists to keep it out of.
  for (const event of [{ type: 'start' }, { type: 'started' }, { type: 'end' }]) {
    assert.deepEqual(await h.session.dispatch(event), {
      ok: false,
      code: SESSION_CODES.NOT_INCOMING,
      field: 'type',
    });
  }
  assert.equal(h.session.state().state, 'listening');
  // A malformed §5 event is VS0.1's refusal, passed through as itself.
  assert.deepEqual(await h.session.dispatch({ type: 'interruptionEnded' }), {
    ok: false,
    code: 'malformed-event',
    field: null,
  });
  assert.equal(h.session.state().state, 'listening');
});

test('a port, a screen or a value that cannot be used is a wiring bug, and it throws', () => {
  const h = harness();
  const good = () => ({
    machine,
    screen: h.screen,
    pipeline: h.pipeline,
    native: h.native,
    now: () => T0,
    resumeTimeoutMs: RESUME_MS,
    title: TITLE,
  });
  const cases = [
    ['no options at all', undefined],
    ['a machine that is not the machine', { ...good(), machine: {} }],
    ['a machine without its first state', { ...good(), machine: { reduce: machine.reduce } }],
    ['a screen without its teardown', { ...good(), screen: { ...h.screen, stop: undefined } }],
    ['a screen without the lease', { ...good(), screen: { ...h.screen, releaseLease: null } }],
    ['a pipeline that is not the pipeline', { ...good(), pipeline: {} }],
    ['a port without updateStatus', { ...good(), native: { ...h.native, updateStatus: null } }],
    ['a port without its mute', { ...good(), native: { ...h.native, setMuted: null } }],
    ['no clock', { ...good(), now: null }],
    ['no window (D201 is the caller’s)', { ...good(), resumeTimeoutMs: undefined }],
    ['a window that is not a duration', { ...good(), resumeTimeoutMs: -1 }],
    ['no title for the activity', { ...good(), title: '' }],
  ];
  for (const [what, options] of cases) {
    assert.throws(() => createVoiceSession(options), TypeError, what);
  }
});
