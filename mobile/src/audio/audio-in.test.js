// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The capture port's tests (mobile plan, Task P3.4; D122, D118, D126).
//
// Twice over: the *shape* the port promises (`isAudioIn`, the event's fields,
// the refusal codes) and the *fake* that every later test drives the pipeline
// with. There is no engine to test -- D123's bake-off is owner-run check 85 and
// is phase 5's -- so what is asserted here is the contract P3.5's screen is
// written against, and the fake's own honesty: a stopped input emits nothing,
// and an event carries the event and nothing heard.

import assert from 'node:assert/strict';
import test from 'node:test';

import { AUDIO_IN_CODES, AUDIO_IN_EVENTS, AUDIO_IN_EVENT_FIELDS, FakeAudioIn, isAudioIn } from './audio-in.js';

test('the port is four methods, and the check says so', () => {
  const fake = new FakeAudioIn();
  assert.equal(isAudioIn(fake), true);
  for (const value of [null, undefined, 42, 'audio', {}, { start: () => {}, stop: () => {}, running: () => {}, on: 'x' }]) {
    assert.equal(isAudioIn(value), false, `${JSON.stringify(value)} is not a port`);
  }
  // One method missing is not a port: a half-shaped object handed to the
  // pipeline's third parameter is the mistake the check exists to catch.
  const { on, ...withoutOn } = { start: () => {}, stop: () => {}, running: () => {}, on: () => {} };
  assert.equal(isAudioIn({ ...withoutOn, oon: on }), false);
});

test('a stopped input emits nothing, and says so', async () => {
  const fake = new FakeAudioIn();
  assert.equal(fake.running(), false);
  for (const result of [fake.speechStart(), fake.speechEnd(), fake.bargeIn()]) {
    assert.deepEqual({ ...result }, { ok: false, code: AUDIO_IN_CODES.NOT_RUNNING, field: 'kind' });
  }
  assert.deepEqual([...fake.emitted()], [], 'a microphone that was never started heard nothing');
});

test('start and stop are idempotent, and say what changed', async () => {
  const fake = new FakeAudioIn();
  assert.deepEqual({ ...(await fake.start()) }, { ok: true, changed: true });
  assert.deepEqual({ ...(await fake.start()) }, { ok: true, changed: false });
  assert.equal(fake.running(), true);
  assert.deepEqual({ ...(await fake.stop()) }, { ok: true, changed: true });
  assert.deepEqual({ ...(await fake.stop()) }, { ok: true, changed: false });
  assert.equal(fake.running(), false);
  assert.deepEqual({ ...fake.calls() }, { starts: 2, stops: 2 });
});

test('D122: the OS refusing the microphone and refusing background audio are different codes', async () => {
  const denied = new FakeAudioIn({ refuse: AUDIO_IN_CODES.MICROPHONE_DENIED });
  const refused = await denied.start();
  assert.equal(refused.ok, false);
  assert.equal(refused.code, 'microphone-denied');
  assert.equal(denied.running(), false, 'a refusal starts nothing');

  const background = new FakeAudioIn({ refuse: AUDIO_IN_CODES.BACKGROUND_AUDIO_DENIED });
  const other = await background.start();
  assert.equal(other.code, 'background-audio-denied');
  assert.notEqual(other.code, refused.code, 'the two denials are what the screen tells apart');

  // ... and a code that is not one of the two is a programming error, not a
  // third state: the fallback is built on exactly these.
  assert.throws(() => new FakeAudioIn({ refuse: 'not-a-denial' }), TypeError);
});

test('D118: an event carries the event, and never anything heard', async () => {
  assert.deepEqual([...AUDIO_IN_EVENT_FIELDS], ['kind']);
  const fake = new FakeAudioIn();
  await fake.start();
  fake.speechStart();
  fake.speechEnd();
  fake.bargeIn();
  const emitted = fake.emitted();
  assert.deepEqual(
    emitted.map((event) => event.kind),
    [...AUDIO_IN_EVENTS],
  );
  for (const event of emitted) {
    assert.deepEqual(Object.keys(event), [...AUDIO_IN_EVENT_FIELDS], 'the field set is the rule');
    assert.equal(Object.isFrozen(event), true);
    assert.equal(Object.isFrozen(emitted), true);
  }
  // The whole list the port may emit is the three events: a fourth would be a
  // fourth thing a device could say, and it would not be covered by P3.5's or
  // the pipeline's rules.
  assert.equal(AUDIO_IN_EVENTS.length, 3);
});

test('a subscriber hears its own kind, and an unsubscribe is idempotent', async () => {
  const fake = new FakeAudioIn();
  const starts = [];
  const ends = [];
  const offStart = fake.on('speech-start', (event) => starts.push(event.kind));
  fake.on('speech-end', (event) => ends.push(event.kind));
  await fake.start();
  fake.speechStart();
  fake.speechEnd();
  offStart();
  offStart(); // twice, on purpose: a screen that tears down twice is not an error
  fake.speechStart();
  assert.deepEqual(starts, ['speech-start']);
  assert.deepEqual(ends, ['speech-end'], 'the speech-end subscriber is unaffected by the other one leaving');
});

test('an unknown event and a handler that is not a function are programming errors', () => {
  const fake = new FakeAudioIn();
  // Thrown rather than refused: `on` always returns a function, and a
  // subscription that silently did not happen is the one outcome a caller
  // cannot check for (see refusal.js).
  // The message, not just the type: without the guard, `handlers.get(undefined)`
  // throws a TypeError too, and a test that only checked the type could not tell
  // the guard from the crash it prevents.
  assert.throws(() => fake.on('speech-pause', () => {}), /unknown event: speech-pause/);
  assert.throws(() => fake.on('speech-start', 'not a function'), /a handler is a function/);
  assert.throws(() => fake.on('speech-start', null), /a handler is a function/);
});
