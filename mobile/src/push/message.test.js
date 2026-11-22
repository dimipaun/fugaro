// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The registration message's field judging: its tests (mobile plan, Task
// P5.2.8; P5.2.1's kind).
//
// The point of this file is that **the app's answers are the schema's**: every
// refusal below is asserted to be the code and the dotted field
// `shared/mailbox-schema` answers with, so an app that accepted a value the
// consumer refuses -- or refused one it accepts -- fails here rather than on a
// phone whose push node never appears.
//
// **No token-shaped literal is written here.** The values under test are either
// obviously synthetic (`'not-a-real-token'`), a repetition whose LENGTH is the
// question, or a string the schema's own `bad-character` rule is about -- and
// none of them is a value any store would hold.

import assert from 'node:assert/strict';
import test from 'node:test';

import { KINDS, REFUSALS } from '../../../shared/mailbox-schema/index.js';
import { CONTROL_DEFAULTS } from './controls.js';
import {
  MESSAGE_CODES,
  REGISTRATION_FIELDS,
  REGISTRATION_KIND,
  REGISTRATION_TEXT,
  judgeField,
} from './message.js';

/** A prefs record as §3.8's defaults are, with a window, as the schema wants it. */
const WINDOW = Object.freeze({ zone: 'Europe/Berlin', start: 1320, end: 420 });

test('the kind the app writes is the schema kind, and it is judged by the name P5.2.1 gave it', () => {
  assert.equal(REGISTRATION_KIND, 'registration');
  assert.equal(KINDS.includes(REGISTRATION_KIND), true, 'the schema carries this kind');
  assert.equal(typeof REGISTRATION_TEXT, 'string');
  assert.notEqual(REGISTRATION_TEXT, '', 'a message with no text is refused by the schema');
  assert.deepEqual([...REGISTRATION_FIELDS], ['token', 'platform', 'prefs']);
});

test('a token the schema accepts is accepted, and one it refuses is refused in its own words', () => {
  assert.equal(judgeField('token', 'not-a-real-token'), null);
  assert.deepEqual(
    { ...judgeField('token', '') },
    { ok: false, code: REFUSALS.EMPTY_STRING, field: 'token' },
    'an empty token is refused, and never written as an empty field',
  );
  assert.deepEqual(
    { ...judgeField('token', null) },
    { ok: false, code: REFUSALS.WRONG_TYPE, field: 'token' },
  );
  assert.deepEqual(
    { ...judgeField('token', 't'.repeat(257)) },
    { ok: false, code: REFUSALS.TOO_LONG, field: 'token' },
  );
  // TOKEN_MAX is the schema's bound, so exactly it must pass: a boundary the
  // app got wrong by one would refuse a legal token.
  assert.equal(judgeField('token', 't'.repeat(256)), null);
});

test('a platform is one of the two the node names', () => {
  assert.equal(judgeField('platform', 'ios'), null);
  assert.equal(judgeField('platform', 'android'), null);
  assert.deepEqual(
    { ...judgeField('platform', 'web') },
    { ok: false, code: REFUSALS.UNKNOWN_PLATFORM, field: 'platform' },
  );
});

test('§3.8’s switches are judged as one record, by the schema’s own field set', () => {
  assert.equal(judgeField('prefs', CONTROL_DEFAULTS), null);
  assert.equal(judgeField('prefs', Object.freeze({ ...CONTROL_DEFAULTS, quiet_hours: WINDOW })), null);
  assert.deepEqual(
    { ...judgeField('prefs', {}) },
    { ok: false, code: REFUSALS.MISSING_FIELD, field: 'prefs.kill_switch' },
    'a switch the app dropped is a refusal, not a default',
  );
  assert.deepEqual(
    { ...judgeField('prefs', { ...CONTROL_DEFAULTS, extra: true }) },
    { ok: false, code: REFUSALS.UNKNOWN_FIELD, field: 'prefs.extra' },
    'the field set IS the version: a switch this build does not have is refused',
  );
  assert.deepEqual(
    { ...judgeField('prefs', null) },
    { ok: false, code: REFUSALS.NOT_AN_OBJECT, field: 'prefs' },
  );
  assert.deepEqual(
    { ...judgeField('prefs', { ...CONTROL_DEFAULTS, kill_switch: 'yes' }) },
    { ok: false, code: REFUSALS.WRONG_TYPE, field: 'prefs.kill_switch' },
  );
  assert.deepEqual(
    { ...judgeField('prefs', { ...CONTROL_DEFAULTS, quiet_hours: { ...WINDOW, end: 1320 } }) },
    { ok: false, code: REFUSALS.BAD_WINDOW, field: 'prefs.quiet_hours.end' },
    'a window with no width is refused, and the field the schema blames is passed through',
  );
});

test('a refusal always blames the field it was asked about', () => {
  // The skeleton is a valid registration, and that is what makes the field
  // question decidable: if it were not, `prefs` would be answered by the
  // skeleton's own `missing-field @ token` and a caller would read it as a bad
  // switch. `probe-refused` is the code for "the skeleton itself was refused",
  // and no value below may produce it.
  const values = ['', null, undefined, 42, {}, [], 'web', 'x'.repeat(300)];
  for (const name of REGISTRATION_FIELDS) {
    for (const value of values) {
      const judged = judgeField(name, value);
      if (judged === null) continue;
      assert.notEqual(judged.code, MESSAGE_CODES.PROBE, `${name} was answered about the skeleton`);
      assert.equal(
        typeof judged.field === 'string' && (judged.field === name || judged.field.startsWith(`${name}.`)),
        true,
        `${name}: the refusal blames ${judged.field}`,
      );
    }
  }
});

test('a field this module does not carry is a wiring bug, not a refusal', () => {
  for (const name of ['text', 'kind', 'mac', 'prefs.kill_switch', '']) {
    assert.throws(() => judgeField(name, true), TypeError, name);
  }
});
