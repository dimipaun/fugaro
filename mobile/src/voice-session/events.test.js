// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The voice session's event contract, and the six kinds it is (voice-session
// plan, Task VS0.1; D205, P8, F9, D213).
//
// **What a test can say about a contract, and what it cannot.** It cannot say
// "both platforms will emit these events" -- no test in this repository can run
// Swift or Kotlin (Global Constraint 10), and the native halves are VS3 and
// VS4. What it *can* say is the thing the two halves are written against: that
// the kinds, the routes, the states and the reasons are the spec's own words,
// that every kind's fields are exactly the ones written down, that a malformed
// event is refused at the boundary, and that none of it can be edited by a
// caller who imported it. That is what makes "identical on both platforms" a
// fact a later task inherits rather than a sentence two native files agree with
// by memory.
//
// **The three refusals the plan names are asserted first**, because they are the
// three a native module is most likely to get wrong: an unknown `type` (a
// platform's own event), an unknown `route` (a platform's own route name), and
// a missing `shouldResume` (the field §6.2 says is always honoured -- an emitter
// that forgets it is an emitter that would resume on `.ended` alone).
//
// **The positive controls are the whole file's shape.** A validator that
// answered `false` to everything would pass every refusal below, so each of the
// six kinds is accepted first, with the optional field both ways, and the
// fields of every event built here are asserted against the map. A scan or a
// validator that cannot say yes is not a check.

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  AUDIO_ROUTES,
  END_REASONS,
  INTERRUPTION_SOURCES,
  PAUSE_REASONS,
  REMOTE_ACTIONS,
  SERVICE_STOP_REASONS,
  SESSION_EVENTS,
  SESSION_EVENT_FIELDS,
  SESSION_STATES,
  isVoiceSessionEvent,
} from './events.js';

test('the six kinds are §5\'s, in the spec\'s order', () => {
  assert.deepEqual(SESSION_EVENTS, [
    'interruptionBegan',
    'interruptionEnded',
    'routeChanged',
    'remoteAction',
    'serviceStopped',
    'error',
  ]);
});

test('every route §5 names is exported, and there are no others', () => {
  assert.deepEqual(AUDIO_ROUTES, ['speaker', 'receiver', 'wired', 'bluetooth', 'usb', 'unknown']);
});

test('the eight states of §4 are exported, in the spec\'s order', () => {
  assert.deepEqual(SESSION_STATES, [
    'idle',
    'starting',
    'listening',
    'speaking',
    'paused_interruption',
    'paused_route',
    'ending',
    'ended',
  ]);
});

test('the sources, the actions, the stop reasons, the two pause reasons and the end reasons are §5\'s words', () => {
  assert.deepEqual(INTERRUPTION_SOURCES, { CALL: 'call', SYSTEM: 'system', OTHER_APP: 'other_app' });
  assert.deepEqual(REMOTE_ACTIONS, { MUTE: 'mute', UNMUTE: 'unmute', HANG_UP: 'hangUp' });
  assert.deepEqual(SERVICE_STOP_REASONS, { SYSTEM: 'system', USER: 'user', ERROR: 'error' });
  // Two paused states, two reasons, and no third: a pause that could be
  // "sometimes" is a pause no screen can render.
  assert.deepEqual(PAUSE_REASONS, { INTERRUPTION: 'interruption', ROUTE: 'route' });
  assert.deepEqual(END_REASONS, {
    HANGUP: 'hangup',
    INTERRUPTION_TIMEOUT: 'interruption_timeout',
    PERMISSION: 'permission',
    ERROR: 'error',
    SERVICE_STOPPED: 'service_stopped',
  });
});

test('the field map covers every kind and no other, and each entry is frozen', () => {
  assert.deepEqual(Object.keys(SESSION_EVENT_FIELDS).sort(), [...SESSION_EVENTS].sort());
  for (const kind of SESSION_EVENTS) {
    assert.ok(Object.isFrozen(SESSION_EVENT_FIELDS[kind]), `${kind}'s fields are frozen`);
    assert.equal(SESSION_EVENT_FIELDS[kind][0], 'type', `${kind} carries its kind first`);
  }
});

test('every kind of §5 is a well-formed event, with its own fields and no others', () => {
  // The positive control for every refusal below: each kind is accepted once,
  // and the event's fields are exactly the kind's (the rule the map states).
  const events = [
    { type: 'interruptionBegan', source: 'call' },
    { type: 'interruptionBegan', source: 'system', suspended: true },
    { type: 'interruptionBegan', source: 'other_app', suspended: false },
    { type: 'interruptionEnded', shouldResume: true },
    { type: 'interruptionEnded', shouldResume: false },
    { type: 'routeChanged', route: 'wired', unsafe: true },
    { type: 'routeChanged', route: 'unknown', unsafe: false },
    { type: 'remoteAction', action: 'mute' },
    { type: 'remoteAction', action: 'unmute' },
    { type: 'remoteAction', action: 'hangUp' },
    { type: 'serviceStopped', reason: 'system' },
    { type: 'serviceStopped', reason: 'user' },
    { type: 'serviceStopped', reason: 'error' },
    { type: 'error', code: 'permission', message: 'the microphone was revoked' },
  ];
  for (const event of events) {
    assert.equal(isVoiceSessionEvent(event), true, JSON.stringify(event));
    assert.deepEqual(
      Object.keys(event).every((key) => SESSION_EVENT_FIELDS[event.type].includes(key)),
      true,
      `${event.type} carries only its own fields`,
    );
  }
  // Every route is a route an event may carry, which is what "every route is
  // exported" has to mean if the list is to be the validator's own.
  for (const route of AUDIO_ROUTES) {
    assert.equal(isVoiceSessionEvent({ type: 'routeChanged', route, unsafe: false }), true, route);
  }
});

test('an unknown type is refused', () => {
  // A platform emitting its own event -- `audioFocusLost`, or a Swift enum's
  // case name -- is not a session event, and is not reduced as one.
  for (const type of ['audioFocusLost', 'interruptionbegan', 'tick', '', undefined]) {
    assert.equal(isVoiceSessionEvent({ type, source: 'call' }), false, String(type));
  }
  // ... and neither is a `type` that names a member of `Object.prototype`: the
  // field map is a plain object, so `constructor` or `toString` would otherwise
  // resolve to an *inherited* value and this check would throw instead of
  // answering no. A validator is handed whatever a platform sends, and a
  // refusal is the only answer it may give.
  for (const type of ['constructor', 'toString', 'valueOf', 'hasOwnProperty', '__proto__', 'isPrototypeOf']) {
    assert.equal(isVoiceSessionEvent({ type, source: 'call' }), false, type);
    assert.equal(isVoiceSessionEvent({ type }), false, type);
  }
  // ... and the shapes that are not events at all.
  for (const value of [null, undefined, 'interruptionBegan', 7, [], true]) {
    assert.equal(isVoiceSessionEvent(value), false, String(value));
  }
});

test('an unknown route is refused', () => {
  for (const route of ['headphones', 'Speaker', '', null, 7]) {
    assert.equal(isVoiceSessionEvent({ type: 'routeChanged', route, unsafe: false }), false, String(route));
  }
  // ... and a route change that does not say whether it is unsafe is refused
  // too: `unsafe` is the whole question §6.3 asks.
  assert.equal(isVoiceSessionEvent({ type: 'routeChanged', route: 'wired' }), false);
  assert.equal(isVoiceSessionEvent({ type: 'routeChanged', route: 'wired', unsafe: 'yes' }), false);
});

test('a missing shouldResume is refused, and a shouldResume that is not a boolean', () => {
  assert.equal(isVoiceSessionEvent({ type: 'interruptionEnded' }), false);
  for (const shouldResume of [undefined, null, 0, 1, 'true']) {
    assert.equal(isVoiceSessionEvent({ type: 'interruptionEnded', shouldResume }), false, String(shouldResume));
  }
});

test('the other kinds refuse a value outside their own vocabulary', () => {
  assert.equal(isVoiceSessionEvent({ type: 'interruptionBegan', source: 'alarm' }), false);
  assert.equal(isVoiceSessionEvent({ type: 'interruptionBegan' }), false);
  // `suspended` is optional and boolean: absent is a real interruption, and a
  // value that is not an answer to "was this the suspension notification" is
  // not an answer at all.
  assert.equal(isVoiceSessionEvent({ type: 'interruptionBegan', source: 'call', suspended: 'yes' }), false);
  assert.equal(isVoiceSessionEvent({ type: 'remoteAction', action: 'resume' }), false);
  assert.equal(isVoiceSessionEvent({ type: 'remoteAction' }), false);
  assert.equal(isVoiceSessionEvent({ type: 'serviceStopped', reason: 'watchdog' }), false);
  assert.equal(isVoiceSessionEvent({ type: 'error', code: '', message: 'x' }), false);
  assert.equal(isVoiceSessionEvent({ type: 'error', code: 'permission' }), false);
  assert.equal(isVoiceSessionEvent({ type: 'error', message: 'no code' }), false);
});

test('a field this kind does not carry is refused, not carried along', () => {
  // The contract's own fence: an extra key is a field a screen could render and
  // a log could hold (F9, D213), so it is refused at the boundary rather than
  // travelling with the event.
  assert.equal(isVoiceSessionEvent({ type: 'interruptionEnded', shouldResume: true, source: 'call' }), false);
  assert.equal(isVoiceSessionEvent({ type: 'remoteAction', action: 'mute', muted: true }), false);
  assert.equal(isVoiceSessionEvent({ type: 'error', code: 'permission', message: 'x', transcript: 'hello' }), false);
});

test('every exported value is frozen, at every depth', () => {
  const exports = {
    AUDIO_ROUTES,
    END_REASONS,
    INTERRUPTION_SOURCES,
    PAUSE_REASONS,
    REMOTE_ACTIONS,
    SERVICE_STOP_REASONS,
    SESSION_EVENTS,
    SESSION_EVENT_FIELDS,
    SESSION_STATES,
  };
  for (const [name, value] of Object.entries(exports)) {
    assert.equal(Object.isFrozen(value), true, `${name} is frozen`);
  }
  for (const kind of SESSION_EVENTS) {
    assert.equal(Object.isFrozen(SESSION_EVENT_FIELDS[kind]), true, `${kind}'s field list is frozen`);
  }
  // Frozen is not a promise, it is a fact: a caller that imported this module
  // cannot add a seventh kind or a seventh route to it.
  assert.throws(() => SESSION_EVENTS.push('tick'), TypeError);
  assert.throws(() => AUDIO_ROUTES.push('carrier-pigeon'), TypeError);
  assert.throws(() => {
    END_REASONS.SILENCE = 'silence';
  }, TypeError);
  assert.throws(() => SESSION_EVENT_FIELDS.remoteAction.push('transcript'), TypeError);
});
