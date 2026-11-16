// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The native port, and the fake that stands in for a device (voice-session
// plan, Task VS1.2; D213, D217, D205, F9, P8).
//
// The plan's Tests line is four sentences and each of them is one test below,
// in its order: *the fake satisfies the same shape assertion the real port
// must* -- "the port is §5's six methods, and the fake is one of them"; *a
// malformed event is refused* -- "a malformed event is refused, with a code and
// the field it names, and nothing that arrived"; *the recorded calls match the
// `updateStatus` throttle* -- "one push per state or mute change, and none for
// a render (D217)"; *the error path carries a code and no free text* -- the
// third test's second half, which is D213's shape seen from the arrival's side.
// The rest is this file's own: the stream's subscribers, the unsubscribe, and
// what the recorded calls and `emitted()` answer while nothing is delivered.
//
// **What is real here.** VS0.2's machine, P3.4's pipeline over its three fakes,
// P3.5's screen and VS1.1's session: the two files this task adds are the only
// new objects, and the throttle is asserted by driving the real session over
// the fake rather than by reading the fake's source and agreeing with it. That
// is the point of the fake -- a device's answers, with no device -- and it is
// why the third test builds the same object `mobile/index.js` will (VS1.3).
//
// **What is deliberately not here.** The port's latency -- a `start()` that
// answers late, an event that lands inside it -- is `session.test.js`'s
// subject, and its own double is built for it (gates, refusals, every call held
// open). The fake is the other half of that pair: deterministic, and honest
// about what it was given, so VS0's and VS1's tests can run a session with no
// phone on the desk. Nothing below reads a clock, a device or a network: `now`
// is a constant, and the whole run is deterministic.

import assert from 'node:assert/strict';
import test from 'node:test';

import { FakeAudioIn, FakeStt, FakeTts, createVoicePipeline } from '../audio/index.js';
import { INTERRUPT_SWITCH, createVoiceScreen } from '../voice/index.js';
import { INTERRUPTION_SOURCES } from './events.js';
import { FakeVoiceSessionNative } from './fake.js';
import * as machine from './machine.js';
import { NATIVE_CODES, NATIVE_METHODS, createNativeEvents, isVoiceSessionNative } from './port.js';
import { createVoiceSession } from './session.js';

const NOW = 1_700_000_000_000;
/** §8's window, handed in: D201's default is the owner's decision and open. */
const RESUME_MS = 180_000;
const TITLE = 'fugaro session';
/** P1.2's holder: the phone's own name for itself. */
const PHONE = 'phone-installation-a';
/** D114's writing identity, in the shape the shared schema accepts. */
const UID = 'AbCdEf0123456789ghIJkl';

/** The `updateStatus` payloads a fake was given, in order. */
const statuses = (native) =>
  native
    .calls()
    .filter((call) => call.name === 'updateStatus')
    .map((call) => call.args[0]);

/** The names of the calls a fake was given, in order. */
const names = (native) => native.calls().map((call) => call.name);

/** The calls of one kind, in order. */
const callsNamed = (native, name) => native.calls().filter((call) => call.name === name);

/**
 * The session `mobile/index.js` will build (VS1.3), with the device replaced:
 * the real machine, the real pipeline over P3.4's fakes, the real screen, and
 * this task's fake where a phone would answer. P3.6's client is a two-line
 * object with the three methods the screen checks for, because this module may
 * not import the real one (F24) and no test below sends anything.
 */
function harness() {
  const pipeline = createVoicePipeline({
    audioIn: new FakeAudioIn(),
    stt: new FakeStt(),
    tts: new FakeTts(),
    sender: UID,
    newId: () => 'm-1',
    now: () => NOW,
  });
  const screen = createVoiceScreen({
    pipeline,
    client: {
      send: async () => ({ ok: true }),
      poll: async () => ({ ok: true }),
      licenceRefusal: async () => null,
    },
    now: () => NOW,
    holder: PHONE,
    switch: INTERRUPT_SWITCH.ON,
  });
  const native = new FakeVoiceSessionNative();
  const session = createVoiceSession({
    machine,
    screen,
    pipeline,
    native,
    now: () => NOW,
    resumeTimeoutMs: RESUME_MS,
    title: TITLE,
  });
  return { session, native };
}

test("the port is §5's six methods, and the fake is one of them", () => {
  // §5's interface, written out: the five the spec names, plus the subscription
  // the event contract arrives on. A seventh is an edit to `NATIVE_METHODS`.
  assert.deepEqual(NATIVE_METHODS, ['start', 'updateStatus', 'setMuted', 'end', 'cleanupStaleSessions', 'on']);

  // A real port's shape: the five methods a platform implements, and the event
  // half of this file bridging the platform's emitter -- which is what
  // `createNativeEvents` is for, and why the check is one list and not two.
  const events = createNativeEvents();
  const port = {
    start: async () => {},
    updateStatus: async () => {},
    setMuted: async () => {},
    end: async () => {},
    cleanupStaleSessions: async () => {},
    on: events.on,
  };
  assert.equal(isVoiceSessionNative(port), true);

  // Each of the six, missing in turn: the mistake this check exists to catch is
  // a port that is *almost* one, because the one that is missing is the one the
  // phone call arrives on.
  for (const name of NATIVE_METHODS) {
    const missing = { ...port };
    delete missing[name];
    assert.equal(isVoiceSessionNative(missing), false, `a port without ${name} is not one`);
  }
  // ... and a value that is not an object at all, including the `null` a wiring
  // file hands over when the module would not load.
  for (const notAPort of [null, undefined, 'VoiceSessionNative', 7, ['start'], Object.freeze({})]) {
    assert.equal(isVoiceSessionNative(notAPort), false, String(notAPort));
  }
  // ... and the fake, which is what every test below runs the session against.
  assert.equal(isVoiceSessionNative(new FakeVoiceSessionNative()), true, 'the fake is a port');
});

test('a malformed event is refused, with a code and the field it names, and nothing that arrived', () => {
  const native = new FakeVoiceSessionNative();
  const seen = [];
  native.on((event) => seen.push(event));

  // The three refusals `events.test.js` names as the likely mistakes, plus the
  // two shapes that are not an event at all. `type` is the field the port can
  // name -- §5's kinds are told apart by it -- and the well-kinded event with a
  // field outside its vocabulary is refused with `null`: the validator answers
  // yes or no, and a port that guessed which field was wrong would be a second
  // copy of `SESSION_EVENT_FIELDS`.
  const refusals = [
    [{ type: 'interruptionBegan' }, null, 'a §5 kind with its source missing'],
    [{ type: 'routeChanged', route: 'speaker' }, null, 'a route change with no `unsafe`'],
    [{ type: 'error', code: '', message: 'the platform said so' }, null, 'an error with no code'],
    [{ type: 'interruption-began', source: 'call' }, 'type', "a platform's own name for a kind"],
    [{ type: 'routeChanged', route: 'headset', unsafe: false }, null, "a platform's own route name"],
    [{ type: 'constructor' }, 'type', 'a kind that names an Object.prototype member'],
    [{ type: 'nonsense', message: 'the words the owner said' }, 'type', 'a kind that is not one'],
    [null, null, 'nothing at all'],
    ['interruptionBegan', null, 'a kind as a bare string'],
    [['interruptionEnded', true], null, 'a kind as an array'],
  ];
  for (const [arrival, field, why] of refusals) {
    const refused = native.emit(arrival);
    assert.equal(refused.ok, false, why);
    assert.equal(refused.code, NATIVE_CODES.NOT_AN_EVENT, why);
    assert.equal(refused.field, field, why);
    // D213's shape, and the fence beside it: a code and the field it names, in
    // `SCREEN_CODES`' manner -- no `message`, no echo of the arrival, and so no
    // sentence a platform wrote and no word the owner said (F9, F25).
    assert.deepEqual(Object.keys(refused).sort(), ['code', 'field', 'ok'], why);
    assert.equal(JSON.stringify(refused).includes('owner said'), false, why);
    assert.equal(JSON.stringify(refused).includes('headset'), false, why);
  }

  // Nothing of that reached a subscriber or the record of what was delivered:
  // a refusal is an arrival that never became an event.
  assert.deepEqual(seen, []);
  assert.deepEqual([...native.emitted()], []);

  // ... and the well-formed ones are delivered, which is what makes the
  // refusals above a judgement about the arrival rather than a port that
  // refuses everything.
  const delivered = native.emit({ type: 'interruptionBegan', source: INTERRUPTION_SOURCES.CALL });
  assert.equal(delivered.ok, true);
  assert.equal(delivered.event.type, 'interruptionBegan');
  assert.equal(Object.isFrozen(delivered.event), true, 'what a subscriber is handed is frozen');
  assert.deepEqual(seen, [delivered.event]);
  assert.deepEqual([...native.emitted()], [delivered.event]);
  // P8's one optional field rides along as §5's kinds do.
  assert.equal(native.emit({ type: 'interruptionBegan', source: 'system', suspended: true }).ok, true);
});

test('the stream reaches every subscriber, and the unsubscribe is idempotent', () => {
  const native = new FakeVoiceSessionNative();
  const first = [];
  const second = [];
  const off = native.on((event) => first.push(event));
  native.on((event) => {
    second.push(event);
    // A subscriber that subscribes while an event is in flight does not hear
    // *that* event: the set is walked over a copy, so who hears an event is
    // decided when it is delivered.
    if (second.length === 1) native.on((event) => second.push(event));
  });

  native.emit({ type: 'interruptionEnded', shouldResume: true });
  assert.equal(first.length, 1);
  assert.equal(second.length, 1, 'the handler subscribed during the delivery did not hear it');

  off();
  native.emit({ type: 'serviceStopped', reason: 'system' });
  assert.equal(first.length, 1, 'an unsubscribed handler hears nothing more');
  assert.equal(second.length, 3, 'the handler, and the one it subscribed, both hear this one');
  assert.equal(off(), false, 'the second unsubscribe removed nobody, and does not throw');

  // A handler that is not a function is a caller that can never work, so it is
  // a throw rather than a refusal -- and no subscription is created by a failed
  // one.
  assert.throws(() => native.on('interruptionEnded'), TypeError);
  assert.throws(() => native.on(null), TypeError);

  // What a subscriber saw is what arrived: a platform that mutates the object
  // it sent, after delivery, changes nothing.
  const arrival = { type: 'routeChanged', route: 'bluetooth', unsafe: false };
  native.emit(arrival);
  arrival.route = 'receiver';
  const heard = [...native.emitted()].at(-1);
  assert.equal(heard.route, 'bluetooth');
  assert.equal(Object.isFrozen(heard), true);
});

test('the fake records every call, in order, with its arguments', async () => {
  const native = new FakeVoiceSessionNative();
  assert.deepEqual([...native.calls()], [], 'a port nothing has asked anything');

  await native.start({ title: TITLE, resumeTimeoutMs: RESUME_MS });
  await native.updateStatus({ state: 'listening', muted: false, startedAt: NOW });
  await native.setMuted(true);
  await native.end('hangup');
  await native.cleanupStaleSessions();

  assert.deepEqual(names(native), ['start', 'updateStatus', 'setMuted', 'end', 'cleanupStaleSessions']);
  assert.deepEqual(callsNamed(native, 'start')[0].args, [{ title: TITLE, resumeTimeoutMs: RESUME_MS }]);
  assert.deepEqual(callsNamed(native, 'updateStatus')[0].args, [
    { state: 'listening', muted: false, startedAt: NOW },
  ]);
  assert.deepEqual(callsNamed(native, 'setMuted')[0].args, [true]);
  assert.deepEqual(callsNamed(native, 'end')[0].args, ['hangup']);
  assert.deepEqual(callsNamed(native, 'cleanupStaleSessions')[0].args, []);
  // The record is a frozen copy, like `FakeAudioIn.calls()`'s counts: a test
  // that mutated it would be asserting against itself.
  assert.equal(Object.isFrozen(native.calls()), true);
  assert.equal(Object.isFrozen(native.calls()[0]), true);
  assert.equal(Object.isFrozen(native.calls()[0].args), true);
});

test('one push per state or mute change, and none for a render (D217)', async () => {
  const { session, native } = harness();
  const verdict = await session.start();
  assert.equal(verdict.ok, true);
  // The port first, and then one push per change: `starting` only once the port
  // has answered (§5's payload carries the `startedAt` a port that has not
  // begun has nothing to render), and `listening` when the microphone is open.
  assert.deepEqual(statuses(native), [
    { state: 'starting', muted: false, startedAt: NOW },
    { state: 'listening', muted: false, startedAt: NOW },
  ]);
  assert.deepEqual(callsNamed(native, 'start')[0].args, [{ title: TITLE, resumeTimeoutMs: RESUME_MS }]);

  // A render changes nothing, and pushes nothing: this is the throttle the spec
  // asks for, and the reason `updateStatus` is not called per frame.
  await session.tick();
  assert.equal(statuses(native).length, 2);

  // A mute change is one more, and it is the same push the native side takes
  // the Live Activity's new `staleDate` from (P5, D214) -- so a session that
  // changes state refreshes it, and a session that is only being rendered does
  // not.
  await session.setMuted(true);
  assert.equal(statuses(native).length, 3);
  assert.deepEqual(statuses(native).at(-1), { state: 'listening', muted: true, startedAt: NOW });

  // ... and a state change is one more, with the reason on the state.
  await session.dispatch({ type: 'interruptionBegan', source: INTERRUPTION_SOURCES.CALL });
  assert.equal(statuses(native).length, 4);
  assert.equal(statuses(native).at(-1).state, 'paused_interruption');

  // §4's ending is not pushed: `end(reason)` is that news, and it carries the
  // cause a state name does not.
  await session.hangUp();
  assert.deepEqual(names(native), [
    'start',
    'updateStatus',
    'updateStatus',
    'setMuted',
    'updateStatus',
    'updateStatus',
    'end',
  ]);
  assert.deepEqual(callsNamed(native, 'end')[0].args, ['hangup']);
  assert.deepEqual(callsNamed(native, 'setMuted')[0].args, [true]);
  // §9's sweep is the app's, on every launch, and not a session's: nothing here
  // calls it, and the fake records it when the app does (VS5.1).
  assert.deepEqual(callsNamed(native, 'cleanupStaleSessions'), []);
  await native.cleanupStaleSessions();
  assert.equal(callsNamed(native, 'cleanupStaleSessions').length, 1);
});
