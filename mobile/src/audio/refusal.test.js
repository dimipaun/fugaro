// The two verdicts, and the check on them (Task P3.4).
//
// `refusal.js` is four lines of shape and this file exists because of the
// fourth: `isRefusal` is exported from the module's door (`index.js`) and is
// therefore something P3.5 will branch on, so it is asserted rather than
// assumed. The one thing worth stating in a test is what it does *not* look at
// -- the code -- because that is the difference between "this is a refusal" and
// "this is the refusal I expected", and only the caller knows the second.

import assert from 'node:assert/strict';
import test from 'node:test';

import { accept, isRefusal, refuse } from './refusal.js';

test('a refusal is a frozen value with a code, and a field when there is one', () => {
  assert.deepEqual({ ...refuse('not-running', 'kind') }, { ok: false, code: 'not-running', field: 'kind' });
  // The field is `null` rather than absent when nothing is at fault, so a
  // caller can always read it (and a `refused` event always carries it).
  assert.deepEqual({ ...refuse('microphone-denied') }, { ok: false, code: 'microphone-denied', field: null });
  assert.equal(Object.isFrozen(refuse('x')), true);
});

test('an acceptance carries what it is given, and is frozen too', () => {
  assert.deepEqual({ ...accept() }, { ok: true });
  assert.deepEqual({ ...accept({ changed: true }) }, { ok: true, changed: true });
  assert.deepEqual(Object.keys(accept({ changed: false })), ['ok', 'changed'], 'the caller\'s fields follow `ok`');
  assert.equal(Object.isFrozen(accept()), true);
});

test('isRefusal tells a refusal from a value that only looks like one', () => {
  assert.equal(isRefusal(refuse('x')), true);
  assert.equal(isRefusal(accept()), false);
  assert.equal(isRefusal({ ok: false, code: 'x' }), true, 'the shape, not the field');
  // The code is not compared: "this is a refusal" is not "this is the refusal I
  // expected". Only the caller knows the second, and a check that guessed would
  // be a check that hides a wrong code.
  assert.equal(isRefusal(refuse('anything at all')), true);
  for (const value of [null, undefined, 42, 'ok:false', {}, { ok: false }, { ok: false, code: 42 }, { ok: true, code: 'x' }]) {
    assert.equal(isRefusal(value), false, `${JSON.stringify(value)} is not a refusal`);
  }
});
