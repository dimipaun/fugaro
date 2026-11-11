// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The session controller's rules (voice-session plan, Task VS1.1; D206, D217,
// F3, F4, F24, D201).
//
// The plan's Tests line is six sentences, and five of the tests below carry them,
// in its order: *start drives both in the right order* -- "start drives the port
// and then the screen..."; *a state change pushes exactly one `updateStatus` and
// a mute change pushes exactly one more* and *a render with no change pushes
// none* -- "a state change pushes one updateStatus, a mute change one more, and a
// render none", which asserts both; *`interruptDecision` sees the pipeline's live
// `speaking`/`turn`* -- "the interrupt decision is handed the pipeline's live
// pair..."; *a paused session issues no mailbox call and reads no credential* --
// "a paused session makes no mailbox call and reads no credential (F24)"; *the
// machine's `ended` releases the lease exactly once* -- "the machine's `ended`
// gives the lease back, exactly once". The rest of the file is this module's own
// -- the snapshot's shape, which the plan does not name, and the port's latency
// with the races and failures it opens.
//
// **What is real here.** VS0.2's machine, P3.4's pipeline over its three fakes,
// and P3.5's screen: the session is the only new object, and it is driven
// through the events the native port will deliver (VS1.2). Two doubles stand in
// for the two things this task does not have -- §5's `VoiceSessionNative`, which
// is VS1.2's file, and P3.6's client, which this module may not import at all
// (F24) and which is therefore a two-line object here rather than the real one.
// Nothing below reads a clock, a device or a network: `now` is a variable a test
// moves (Global Constraint 10).
//
// **The port's latency is a state of its own.** `native.start()` and
// `native.setMuted()` answer when they answer, and an event can arrive before
// they do. The race tests below hold one of those calls open and move the session
// under it, because that is the only way a verdict reduced before a call, or a
// screen started after one, is seen to be wrong.

import assert from 'node:assert/strict';
import test from 'node:test';

import { SEVERITY } from '../../../shared/digest/index.js';
import { emptyLeaseState } from '../../../shared/lease/index.js';
import { FakeAudioIn, FakeStt, FakeTts, createVoicePipeline } from '../audio/index.js';
import { INTERRUPT_SWITCH, createVoiceScreen } from '../voice/index.js';
import { INTERRUPTION_SOURCES, REMOTE_ACTIONS, SERVICE_STOP_REASONS } from './events.js';
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
 * A port call the test holds open: the window in which the session can move
 * while a call is in flight. Both of the races below are that window -- an event
 * arriving between the port being asked and the port answering -- and a promise
 * the test opens by hand is the only way to put one in it without a timer.
 */
function gate() {
  let open;
  let fail;
  const promise = new Promise((resolve, reject) => {
    open = resolve;
    fail = reject;
  });
  return { promise, open: () => open(), fail: (error) => fail(error) };
}

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
 * @param {boolean} [options.refuseStatus] make `updateStatus` reject
 * @param {object} [options.holdStart] a `gate()` `start` waits on before it
 *   answers: the session's own events arriving while the port is starting
 * @param {object} [options.holdFirstStart] a `gate()` only the **first**
 *   `start` waits on, so that a second session can begin while the first call is
 *   still unanswered: the window a `start()` the caller has abandoned drives
 * @param {object} [options.holdEnd] a `gate()` `end` waits on: a teardown that
 *   is still running when the next session asks to begin
 * @param {object} [options.holdSetMuted] a `gate()` `setMuted` waits on
 * @param {() => boolean} [options.microphoneOpen] what each call records
 */
function nativeDouble({
  refuseStart = false,
  refuseEnd = false,
  refuseStatus = false,
  holdStart = null,
  holdFirstStart = null,
  holdEnd = null,
  holdSetMuted = null,
  microphoneOpen = null,
} = {}) {
  const calls = [];
  let starts = 0;
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
      starts += 1;
      return record('start', [options], () => {
        if (refuseStart) return Promise.reject(new Error('no'));
        if (holdFirstStart !== null && starts === 1) return holdFirstStart.promise;
        return holdStart?.promise ?? Promise.resolve();
      });
    },
    updateStatus(status) {
      return record('updateStatus', [status], () =>
        refuseStatus ? Promise.reject(new Error('no')) : Promise.resolve(),
      );
    },
    setMuted(muted) {
      return record('setMuted', [muted], () => holdSetMuted?.promise ?? Promise.resolve());
    },
    end(reason) {
      return record('end', [reason], () =>
        refuseEnd ? Promise.reject(new Error('no')) : (holdEnd?.promise ?? Promise.resolve()),
      );
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
 *
 * @param {number} [options.holdScreenStartAfter] how many of the screen's starts
 *   to let through before `holdScreenStart` holds them (0: every one)
 * @param {number} [options.refuseScreenStartAfter] how many of the screen's
 *   starts to let through before `refuseScreenStart` throws (0: every one), so
 *   that a *later* start -- a resume's -- is the one that fails
 */
function harness({
  refuseStart = false,
  refuseEnd = false,
  refuseStatus = false,
  refuseScreenStart = false,
  refuseScreenStartAfter = 0,
  refuseScreenStop = false,
  holdStart = null,
  holdFirstStart = null,
  holdEnd = null,
  holdSetMuted = null,
  holdScreenStart = null,
  holdScreenStartAfter = 0,
  client = null,
} = {}) {
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
  let screenStarts = 0;
  const screen = {
    ...built,
    releaseLease: () => {
      releases += 1;
      return built.releaseLease();
    },
    // P3.4's pipeline opens the microphone behind this, so holding or refusing
    // it is how the window between `native.start()` answering and the
    // microphone being open is put in a test's hands. `holdScreenStartAfter`
    // lets the session's own start through first, which is what a *resume*
    // needs: the start under test is the one the resume makes.
    start: async () => {
      screenStarts += 1;
      if (holdScreenStart !== null && screenStarts > holdScreenStartAfter) await holdScreenStart.promise;
      if (refuseScreenStart && screenStarts > refuseScreenStartAfter) throw new Error('the capture path would not start');
      return built.start();
    },
    // ...and the teardown closes it behind this, which is how a microphone that
    // will not close is put in one.
    stop: async () => {
      if (refuseScreenStop) throw new Error('the capture path would not stop');
      return built.stop();
    },
  };
  const native = nativeDouble({
    refuseStart,
    refuseEnd,
    refuseStatus,
    holdStart,
    holdFirstStart,
    holdEnd,
    holdSetMuted,
    microphoneOpen: () => audioIn.running(),
  });
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
  assert.deepEqual(h.mailbox.calls.map((call) => call.name), ['send']);
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
    'failure',
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
  // §4's paused states are "mic closed": the call is not transcribed, which is
  // the whole of F24 -- the capture path is not running, so the speech this test
  // puts through it is refused rather than becoming a message.
  assert.equal(h.audioIn.running(), false, 'the microphone is closed for the pause');
  assert.deepEqual(
    h.audioIn.speechStart(),
    { ok: false, code: 'not-running', field: 'kind' },
    'nothing is captured through a pause',
  );
  await settle();
  assert.equal(h.stt.listening(), false, 'and the engine holds no turn to make a message of');

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
  // The resume is what opens the microphone again, and it is still no mailbox
  // call: the store stays unread while the phone is locked.
  assert.equal(h.audioIn.running(), true);

  assert.deepEqual(h.mailbox.calls, [], 'nothing reached the store while the phone was locked');
});

test('a route pause stops the utterance in flight and closes the microphone (§6.3)', async () => {
  const h = harness();
  await h.session.start();
  h.screen.setDigest({ severity: SEVERITY.kill });
  const update = { text: 'the nightly build failed', severity: SEVERITY.kill };
  h.screen.offer({ ...update });
  assert.equal(h.tts.speaking(), true, 'the session is speaking when the route goes');

  await h.session.dispatch({ type: 'routeChanged', route: 'speaker', unsafe: true });
  assert.equal(h.session.state().state, 'paused_route');
  assert.equal(h.session.state().pauseReason, 'route');
  // §6.3: it "must not continue speaking through the loudspeaker" -- and §4's
  // paused states are "mic closed", so neither half of the pipeline is running.
  assert.equal(h.tts.speaking(), false, 'the utterance is cancelled rather than finished out loud');
  assert.equal(h.audioIn.running(), false, 'the microphone is closed for the pause');

  // §4: a route pause resumes only on an explicit action, and that resume is
  // what opens the microphone again.
  await h.session.resume();
  assert.equal(h.session.state().state, 'listening');
  assert.equal(h.audioIn.running(), true);
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

test('a session that ends while the port is starting never opens the microphone', async () => {
  // §9: the platform stops the service while `native.start()` is still in
  // flight. The session is over before the port answers -- and the teardown that
  // event would have run found no port, because `begun` was still false.
  const starting = gate();
  const h = harness({ holdStart: starting });
  const begun = h.session.start();
  await settle();
  assert.deepEqual(h.native.calls.map((call) => call.name), ['start'], 'nothing follows the port until it answers');

  await h.session.dispatch({ type: 'serviceStopped', reason: SERVICE_STOP_REASONS.SYSTEM });
  assert.equal(h.session.state().state, 'ended');
  assert.equal(h.session.state().endReason, 'service_stopped');

  starting.open();
  assert.deepEqual(await begun, { ok: false, code: SESSION_CODES.START_FAILED, field: null });
  assert.equal(h.audioIn.calls().starts, 0, 'no microphone is opened for a session that is already over');
  assert.equal(h.audioIn.running(), false);
  assert.deepEqual(callsNamed(h.native, 'end').map((call) => call.args[0]), ['service_stopped']);
});

test('a hang-up while the port is starting ends the port, and starts no screen', async () => {
  const starting = gate();
  const h = harness({ holdStart: starting });
  const begun = h.session.start();
  await settle();

  // §4: the Live Activity's hang-up arrives while the port is starting. The
  // machine is in `ending`, and `ending` waits for an `end` -- which is this
  // module's, so it happens once the port has answered.
  await h.session.hangUp();
  assert.equal(h.session.state().state, 'ending');

  starting.open();
  assert.deepEqual(await begun, { ok: false, code: SESSION_CODES.START_FAILED, field: null });
  assert.equal(h.session.state().state, 'ended');
  assert.equal(h.session.state().endReason, 'hangup');
  assert.equal(h.audioIn.calls().starts, 0);
  assert.deepEqual(callsNamed(h.native, 'end').map((call) => call.args[0]), ['hangup']);
  assert.deepEqual(statuses(h.native), [], 'a session that never reached `started` is never pushed');
});

test('a mute whose port call is in flight does not roll the session back', async () => {
  const muting = gate();
  const h = harness({ holdSetMuted: muting });
  await h.session.start();

  const muted = h.session.setMuted(true);
  await settle();
  assert.deepEqual(callsNamed(h.native, 'setMuted').map((call) => call.args[0]), [true]);

  // The hang-up arrives while the port is being muted. The mute's verdict was
  // reduced from `listening`, and committing it now would answer a hang-up with
  // `listening` again -- with `endReason` back to null.
  await h.session.hangUp();
  assert.equal(h.session.state().state, 'ended');

  muting.open();
  assert.equal((await muted).ok, false, 'the mute does not land on a session that has ended');
  assert.equal(h.session.state().state, 'ended', 'the hang-up is not rolled back');
  assert.equal(h.session.state().endReason, 'hangup');
  assert.deepEqual(
    statuses(h.native).map((status) => status.state),
    ['starting', 'listening'],
    'a state the session is not in is never pushed',
  );
});

test('a mute in flight keeps the pause §8’s timeout is measured from', async () => {
  const muting = gate();
  const h = harness({ holdSetMuted: muting });
  await h.session.start();

  const muted = h.session.setMuted(true);
  await settle();

  // The interruption arrives while the port is being muted: the pause and the
  // instant §8's timer runs from are created after the verdict the mute is
  // holding.
  await h.session.dispatch({ type: 'interruptionBegan', source: INTERRUPTION_SOURCES.CALL });
  assert.equal(h.session.state().pauseReason, 'interruption');

  muting.open();
  await muted;
  assert.equal(h.session.state().state, 'paused_interruption');
  assert.equal(h.session.state().pauseReason, 'interruption');
  assert.equal(h.session.state().pausedAt, T0, 'the pause keeps the instant §8 measures from');
  assert.equal(h.session.state().muted, true, 'the mute lands on the state the session is in');

  // ...so the window still ends the session when it passes.
  h.advance(RESUME_MS + 1);
  await h.session.tick();
  assert.equal(h.session.state().state, 'ended');
  assert.equal(h.session.state().endReason, 'interruption_timeout');
  assert.deepEqual(callsNamed(h.native, 'end').map((call) => call.args[0]), ['interruption_timeout']);
});

test('a capture path that rejects ends the session instead of stranding it', async () => {
  // The pipeline answers D122's refusal rather than throwing, so this is the
  // other kind of capture failure -- and it must not leave a session in
  // `starting` with a port that began and nothing that ends it.
  const h = harness({ refuseScreenStart: true });
  const verdict = await h.session.start();

  assert.deepEqual(verdict, { ok: false, code: SESSION_CODES.START_FAILED, field: null });
  assert.equal(h.session.state().state, 'ended');
  assert.deepEqual(h.session.state().error, {
    code: SESSION_CODES.START_FAILED,
    message: 'the voice session could not start',
  });
  assert.deepEqual(callsNamed(h.native, 'end').map((call) => call.args[0]), ['error'], 'the port that began is ended');
});

test('a session that ends while the microphone is opening closes it again', async () => {
  // The window on the other side of `native.start()`: the port has answered, the
  // screen is starting, and the session ends under it -- so the teardown stopped
  // a screen that had not opened the microphone yet, and the microphone opens
  // after it. It is closed again here, and the session did not start.
  const opening = gate();
  const h = harness({ holdScreenStart: opening });
  const begun = h.session.start();
  await settle();
  assert.equal(h.audioIn.running(), false, 'nothing is capturing while the screen is starting');

  await h.session.dispatch({ type: 'serviceStopped', reason: SERVICE_STOP_REASONS.SYSTEM });
  assert.equal(h.session.state().state, 'ended');
  assert.deepEqual(callsNamed(h.native, 'end').map((call) => call.args[0]), ['service_stopped']);

  opening.open();
  assert.deepEqual(await begun, { ok: false, code: SESSION_CODES.START_FAILED, field: null });
  assert.equal(h.audioIn.running(), false, 'the microphone the raced start opened is closed again');
  assert.equal(h.session.state().state, 'ended');
});

test('a resume racing a hang-up does not leave the microphone open', async () => {
  // A route pause resumes on an explicit action, and the screen's start is
  // asynchronous. A hang-up arriving while that start is in flight is the same
  // shape as the windows above -- except that here the two screen calls are on
  // different paths, so the teardown's `stop()` has to be ordered behind the
  // resume's `start()` rather than racing it: otherwise the start lands last and
  // the session is over with the microphone open.
  const opening = gate();
  const h = harness({ holdScreenStart: opening, holdScreenStartAfter: 1 });
  await h.session.start();
  await h.session.dispatch({ type: 'routeChanged', route: 'speaker', unsafe: true });
  assert.equal(h.session.state().state, 'paused_route');
  assert.equal(h.audioIn.running(), false, 'the pause closed it');

  const resuming = h.session.resume();
  await settle();
  assert.equal(h.audioIn.running(), false, 'the resume is still opening the microphone');

  const ending = h.session.hangUp();
  await settle();
  opening.open();
  await resuming;
  await ending;

  assert.equal(h.session.state().state, 'ended');
  assert.equal(h.audioIn.running(), false, 'the microphone the raced resume opened is closed again');
});

test('a pause that arrives while the microphone is opening leaves it closed', async () => {
  // The same window, with §4's pause instead of an ending: the port has answered,
  // the screen is starting, and the interruption arrives under it. A paused
  // session is "mic closed", so the microphone this start went on to open is
  // closed again -- a session inside a phone call is not listening to it.
  const opening = gate();
  const h = harness({ holdScreenStart: opening });
  const begun = h.session.start();
  await settle();
  assert.equal(h.audioIn.running(), false, 'nothing is capturing while the screen is starting');

  await h.session.dispatch({ type: 'interruptionBegan', source: INTERRUPTION_SOURCES.CALL });
  assert.equal(h.session.state().state, 'paused_interruption');

  opening.open();
  const verdict = await begun;
  assert.equal(verdict.ok, false, 'the machine refuses a `started` for a session that is paused');
  assert.equal(h.session.state().state, 'paused_interruption', 'and the start does not undo the pause');
  assert.equal(h.audioIn.running(), false, 'the microphone the raced start opened is closed again');

  // ...and the resume that ends the pause is still what opens it.
  await h.session.dispatch({ type: 'interruptionEnded', shouldResume: true });
  assert.equal(h.session.state().state, 'listening');
  assert.equal(h.audioIn.running(), true);
});

test('a session paused while the port is answering never opens the microphone', async () => {
  // The window before that one: the interruption arrives while `native.start()`
  // is in flight, so the port answers a session that is already paused. §4's
  // paused states are "mic closed", so the screen is never started into one --
  // and the session is alive and paused, with the port told so, rather than
  // reported as one that failed to start.
  const starting = gate();
  const h = harness({ holdStart: starting });
  const begun = h.session.start();
  await settle();

  await h.session.dispatch({ type: 'interruptionBegan', source: INTERRUPTION_SOURCES.CALL });
  assert.equal(h.session.state().state, 'paused_interruption');

  starting.open();
  const verdict = await begun;
  assert.equal(verdict.ok, false, 'the machine refuses a `started` for a session that is not `starting`');
  assert.equal(h.audioIn.calls().starts, 0, 'no microphone is opened inside the call that paused the session');
  assert.equal(h.audioIn.running(), false);
  assert.equal(h.session.state().state, 'paused_interruption', 'the session is paused, not over');
  assert.deepEqual(
    statuses(h.native).map((status) => status.state),
    ['paused_interruption'],
    'the port is told what the session is',
  );

  await h.session.dispatch({ type: 'interruptionEnded', shouldResume: true });
  assert.equal(h.session.state().state, 'listening');
  assert.equal(h.audioIn.running(), true, 'and the resume opens the microphone it never opened before');
});

test('an interruption that ends under a port call does not open the microphone before the port answered', async () => {
  // The window inside that one: the pause and the resume that ends it both
  // arrive while `native.start()` is still answering, so the session is back to
  // `listening` with no port under it. The resume's crossing is not the screen's
  // to act on there -- the session it would capture for is one the port does not
  // have yet -- and `start()` opens the microphone when the port answers, which
  // is the order every other path keeps.
  const starting = gate();
  const h = harness({ holdStart: starting });
  const begun = h.session.start();
  await settle();

  await h.session.dispatch({ type: 'interruptionBegan', source: INTERRUPTION_SOURCES.CALL });
  await h.session.dispatch({ type: 'interruptionEnded', shouldResume: true });
  assert.equal(h.session.state().state, 'listening');
  assert.equal(h.audioIn.calls().starts, 0, 'the port has not answered, so nothing is capturing');

  starting.open();
  const verdict = await begun;
  assert.equal(verdict.ok, true, 'the session is listening, so it is not reported as one that failed to start');
  assert.equal(h.audioIn.calls().starts, 1, 'and the screen is started once, after the port');
  assert.equal(h.audioIn.running(), true);
});

test('a route pause and its resume under a port call are not started twice', async () => {
  // The same window with §6.3's pause, which resumes on an explicit action: the
  // screen is started for neither half of it before the port answers, so the
  // port's own answer starts it once rather than once per crossing -- and the
  // session it finds is already listening -- which is what the caller is told: a
  // `started` driven on that session is refused with `no-transition`, and that
  // refusal is not the answer to "did the session start".
  const starting = gate();
  const h = harness({ holdStart: starting });
  const begun = h.session.start();
  await settle();

  await h.session.dispatch({ type: 'routeChanged', route: 'speaker', unsafe: true });
  assert.equal(h.session.state().state, 'paused_route');
  await h.session.resume();
  assert.equal(h.session.state().state, 'listening');
  assert.equal(h.audioIn.calls().starts, 0, 'the resume opens nothing before the port has answered');

  starting.open();
  const verdict = await begun;
  assert.equal(verdict.ok, true, 'the session is running, so `start` does not answer that it did not start');
  assert.equal(h.session.state().state, 'listening');
  assert.equal(h.audioIn.calls().starts, 1);
  assert.equal(h.audioIn.running(), true);
});

test('a new session waits for the last one’s teardown before it begins', async () => {
  // §8's "Tap to resume" is a `start()` on a session that ended on its own, and
  // that session's teardown may still be running: the transition that ends a
  // session does not wait for the port's `end()`. A session begun under it would
  // have the last one's `screen.stop()` and `native.end()` land on it.
  const ending = gate();
  const h = harness({ holdEnd: ending });
  await h.session.start();
  // Not awaited: the teardown it starts is held, which is the state a resume
  // meets. The state has already moved by the time this call returns.
  const stopped = h.session.dispatch({ type: 'serviceStopped', reason: SERVICE_STOP_REASONS.SYSTEM });
  await settle();
  assert.equal(h.session.state().state, 'ended');
  assert.deepEqual(callsNamed(h.native, 'end').map((call) => call.args[0]), ['service_stopped']);

  const again = h.session.start();
  await settle();
  assert.equal(h.session.state().state, 'ended', 'the new session has not begun under the last one’s teardown');
  assert.equal(callsNamed(h.native, 'start').length, 1, 'and the port has not been asked for a second session');

  ending.open();
  assert.equal((await again).ok, true);
  await stopped;
  assert.equal(h.session.state().state, 'listening');
  assert.equal(h.audioIn.running(), true, 'the microphone is the new session’s to open');
  assert.equal(callsNamed(h.native, 'start').length, 2);
});

test('a start the caller abandoned does not end the session that replaced it', async () => {
  // The first `start()` is still waiting for the port when an event ends the
  // session -- before `begun`, so nothing was torn down -- and a second `start()`
  // resets and begins. The first call's rejection belongs to the session that is
  // gone: applying §5's `error` from it would end a session that has just begun
  // and tear the port down under it.
  const first = gate();
  const h = harness({ holdFirstStart: first });
  const abandoned = h.session.start();
  await settle();

  await h.session.dispatch({ type: 'serviceStopped', reason: SERVICE_STOP_REASONS.SYSTEM });
  assert.equal(h.session.state().state, 'ended');

  const begun = h.session.start();
  assert.equal((await begun).ok, true);
  assert.equal(h.session.state().state, 'listening');

  first.fail(new Error('the port was not there any more'));
  assert.deepEqual(await abandoned, { ok: false, code: SESSION_CODES.START_FAILED, field: null });
  assert.equal(h.session.state().state, 'listening', 'the abandoned start does not end the new session');
  assert.equal(h.session.state().error, null, 'its failure is not §5’s `error` for the session that is running');
  assert.deepEqual(callsNamed(h.native, 'end'), [], 'and nothing is torn down under it');
});

test('a start the caller abandoned does not open the microphone for the session that replaced it', async () => {
  const first = gate();
  const h = harness({ holdFirstStart: first });
  const abandoned = h.session.start();
  await settle();

  await h.session.dispatch({ type: 'serviceStopped', reason: SERVICE_STOP_REASONS.SYSTEM });
  const begun = h.session.start();
  assert.equal((await begun).ok, true);
  assert.equal(h.audioIn.running(), true);

  // The port answers the abandoned call at last. It is not this session's --
  // `start()`'s per-session check gives up on it -- so it neither opens a
  // microphone for it nor drives a `started` on its behalf.
  first.open();
  assert.deepEqual(await abandoned, { ok: false, code: SESSION_CODES.START_FAILED, field: null });
  assert.equal(h.audioIn.calls().starts, 1, 'the screen is started once, for the session that is running');
  assert.equal(h.audioIn.running(), true, 'and the microphone that is open is the running session’s');
});

test('a start abandoned with the microphone opening closes it for the session that replaced it', async () => {
  // The other half of that fence, and the one the fence cannot cover on its own:
  // there the abandoned call's `native.start()` never answered, so it had no
  // microphone to open; here it is the *screen* start that is still in flight when
  // the session ends, and the start it goes on to finish opens a microphone for a
  // session this call no longer holds. A session that is over or paused is not
  // listening, whoever opened the microphone -- F24 is a call transcribed and
  // sent, and the session that would send it is the one the paused screen holds.
  const first = gate();
  const second = gate();
  const opening = gate();
  const h = harness({ holdFirstStart: first, holdStart: second, holdScreenStart: opening });

  const abandoned = h.session.start();
  await settle();
  first.open();
  await settle();
  assert.equal(h.session.state().state, 'starting');
  assert.equal(h.audioIn.running(), false, 'nothing is capturing while the screen start is held');

  // The session this call belongs to ends, and §8's "Tap to resume" begins the
  // next one in its place -- while the first call is still inside the screen.
  await h.session.hangUp();
  assert.equal(h.session.state().state, 'ended');
  const replaced = h.session.start();
  await settle();
  // The replacement is paused before it reaches its own screen call, so the
  // microphone the abandoned start is about to open is the only one there is.
  await h.session.dispatch({ type: 'routeChanged', route: 'speaker', unsafe: true });
  second.open();
  assert.equal((await replaced).ok, false, 'the machine refuses a `started` for a session that is paused');
  assert.equal(h.session.state().state, 'paused_route');

  opening.open();
  assert.deepEqual(await abandoned, { ok: false, code: SESSION_CODES.START_FAILED, field: null });
  assert.equal(h.audioIn.running(), false, 'the microphone the abandoned start opened is closed again');
});

test('a start abandoned while the session is starting is not left holding a microphone on it', async () => {
  // The window the per-session fence exists for, and the one thing the fence
  // cannot do on its own. The abandoned call opens the microphone as it gives up,
  // and the session it opened it for has not reached its own screen call yet -- it
  // is still `starting` -- so no path of that session's own closes it. A session
  // that then ends before its port answers has no teardown of its own either
  // (`begun` is false), which is how a session ends with the microphone running.
  const first = gate();
  const second = gate();
  const opening = gate();
  const h = harness({ holdFirstStart: first, holdStart: second, holdScreenStart: opening });

  const abandoned = h.session.start();
  await settle();
  first.open();
  await settle();
  assert.equal(h.session.state().state, 'starting');

  await h.session.hangUp();
  assert.equal(h.session.state().state, 'ended');

  // §8's "Tap to resume" begins the next session, whose own port call is still
  // unanswered: it is `starting`, and has not reached its own screen start.
  const replaced = h.session.start();
  await settle();
  assert.equal(h.session.state().state, 'starting');

  opening.open();
  assert.deepEqual(await abandoned, { ok: false, code: SESSION_CODES.START_FAILED, field: null });
  assert.equal(h.session.state().state, 'starting', 'the abandoned start does not drive the session that replaced it');
  assert.equal(h.audioIn.running(), true, 'and the microphone it opened is open all the same');

  // ...and that session ends before its port answers, so the microphone the
  // abandoned call opened is this module's to close.
  await h.session.dispatch({ type: 'serviceStopped', reason: SERVICE_STOP_REASONS.SYSTEM });
  assert.equal(h.session.state().state, 'ended');
  assert.equal(h.audioIn.running(), false, 'the microphone the abandoned start opened is closed');
  assert.deepEqual(
    callsNamed(h.native, 'end').map((call) => call.args[0]),
    ['hangup'],
    'and the session that never began is not asked to end',
  );
  // `replaced` is deliberately never answered: the reproduction is a session that
  // ends before its own port call does.
});

test('a port that answers after the teardown has finished is ended all the same', async () => {
  // The other half of the test above, and the one it stops short of. That session
  // ended before its port answered, so the teardown had no port half to run -- and
  // the answer that arrives afterwards is a session this module asked for: a
  // foreground service and a Live Activity that nothing else would stop, on a
  // session the caller reads as over.
  const first = gate();
  const second = gate();
  const opening = gate();
  const h = harness({ holdFirstStart: first, holdStart: second, holdScreenStart: opening });

  const abandoned = h.session.start();
  await settle();
  first.open();
  await settle();

  await h.session.hangUp();
  assert.equal(h.session.state().state, 'ended');

  const replaced = h.session.start();
  await settle();
  opening.open();
  await settle();
  await h.session.dispatch({ type: 'serviceStopped', reason: SERVICE_STOP_REASONS.SYSTEM });
  assert.equal(h.session.state().state, 'ended');
  assert.equal(h.audioIn.running(), false, 'the microphone the abandoned start opened is closed');
  assert.deepEqual(
    callsNamed(h.native, 'end').map((call) => call.args[0]),
    ['hangup'],
    'nothing was asked of a port that had not begun',
  );

  // The port answers at last, and the session it begins is one that is already
  // over: the teardown that ended it is finished and cannot end it now.
  second.open();
  assert.deepEqual(await abandoned, { ok: false, code: SESSION_CODES.START_FAILED, field: null });
  assert.deepEqual(await replaced, { ok: false, code: SESSION_CODES.START_FAILED, field: null });
  assert.deepEqual(
    callsNamed(h.native, 'end').map((call) => call.args[0]),
    ['hangup', 'service_stopped'],
    'the port that began after the teardown is ended by the call that heard it begin',
  );
  assert.equal(h.session.state().state, 'ended');
});

test('a pause whose screen will not stop records pause-failed, and stays paused', async () => {
  const h = harness({ refuseScreenStop: true });
  await h.session.start();
  await h.session.dispatch({ type: 'interruptionBegan', source: INTERRUPTION_SOURCES.CALL });

  assert.equal(h.session.state().state, 'paused_interruption');
  // A capture path that throws is a microphone that may still be open, which is
  // why the pause records it instead of the session reading as one that closed
  // it: the session is paused and the failure is on the snapshot.
  assert.equal(h.audioIn.running(), true, 'the stop that threw stopped nothing');
  assert.deepEqual(h.session.state().failure, {
    code: SESSION_CODES.PAUSE_FAILED,
    message: 'the microphone could not be closed for the pause',
  });
});

test('a resume whose screen will not start records resume-failed, and stays listening', async () => {
  const h = harness({ refuseScreenStart: true, refuseScreenStartAfter: 1 });
  await h.session.start();
  await h.session.dispatch({ type: 'routeChanged', route: 'speaker', unsafe: true });
  assert.equal(h.session.state().state, 'paused_route');

  await h.session.resume();

  assert.equal(h.session.state().state, 'listening');
  assert.equal(h.audioIn.running(), false, 'the microphone the resume could not open is closed');
  assert.deepEqual(h.session.state().failure, {
    code: SESSION_CODES.RESUME_FAILED,
    message: 'the microphone could not be opened on resume',
  });
});

test('a teardown that fails is on the snapshot whichever way the session ended', async () => {
  // §8's timeout reaches `ended` on its own, and the machine takes no event for a
  // session that is already over -- so the teardown failure has to be this
  // module's own field, or it is reported on a hang-up and nowhere else.
  const h = harness({ refuseEnd: true });
  await h.session.start();
  await h.session.dispatch({ type: 'interruptionBegan', source: INTERRUPTION_SOURCES.CALL });
  h.advance(RESUME_MS + 1);
  await h.session.tick();

  assert.equal(h.session.state().state, 'ended');
  assert.equal(h.session.state().endReason, 'interruption_timeout');
  assert.deepEqual(h.session.state().failure, {
    code: SESSION_CODES.END_FAILED,
    message: 'the voice session could not tear down',
  });
  assert.equal(h.session.state().error, null, 'the machine keeps its own `error` for §5’s error events');
});

test('a status the port would not take is not an unhandled rejection', async () => {
  const h = harness({ refuseStatus: true });
  await h.session.start();
  // Two turns of the microtask queue: the port's rejection is this module's to
  // handle, and an unhandled one fails the whole file rather than this test.
  await settle();
  await settle();
  assert.equal(h.session.state().state, 'listening', 'a status a screen draws moves nothing');
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
    ['starting', 'listening', 'paused_interruption'],
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

test('a microphone that will not close still ends the port', async () => {
  // The teardown is two calls and the second is the one that takes the
  // foreground service and the Live Activity down. A screen that will not stop
  // is a microphone that may still be open -- and it is no reason to leave the
  // service alive for a session this module reports as over.
  const h = harness({ refuseScreenStop: true });
  await h.session.start();
  await h.session.hangUp();

  assert.equal(h.session.state().state, 'ended');
  assert.deepEqual(
    callsNamed(h.native, 'end').map((call) => call.args[0]),
    ['hangup'],
    'the port is ended although the screen would not stop',
  );
  assert.deepEqual(h.session.state().failure, {
    code: SESSION_CODES.END_FAILED,
    message: 'the voice session could not tear down',
  });
});

test('a port that will not end is reported, and the session still ends', async () => {
  const h = harness({ refuseEnd: true });
  await h.session.start();
  await h.session.hangUp();

  // The cause is kept and the failure is on top of it: "why did this end" is
  // still the hang-up, and the teardown that did not finish is what the screen
  // shows beside it. It is the session's own field, not the machine's `error`,
  // which is §5's error events -- this one happens after the session is over.
  assert.equal(h.session.state().state, 'ended');
  assert.equal(h.session.state().endReason, 'hangup');
  assert.deepEqual(h.session.state().failure, {
    code: SESSION_CODES.END_FAILED,
    message: 'the voice session could not tear down',
  });
  assert.equal(h.session.state().error, null);
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
