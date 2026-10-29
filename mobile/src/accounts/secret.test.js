// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The two items and the unprintable value: their tests (Task P3.1, D111).
//
// D111 lists exactly two items and says both are **never logged, never in a
// fixture and never in a settings file**. A rule like that is worth nothing
// unless something enforces it, so this file tests the three ways a secret
// escapes by accident -- printing it, serialising it, and enumerating the
// object that holds it -- and its own positive control: the same three
// conversions on a plain string DO carry the value, so a green run here cannot
// mean "the assertion was vacuous".
//
// The values below are synthetic and shaped nothing like a credential: a
// literal that looked like a token would be exactly the fixture D111 forbids,
// and `mobile/README.md` records that no value in this directory is recorded
// from a real installation (`belong` least of all -- Global Constraint 11).
//
// What else is pinned here:
//
//   * `SECRET_ITEMS` is D111's list and the WHOLE list: a third name is not
//     storable, because `secure-store.js` refuses a name that is not on it;
//   * the value's rules are a boundary on both sides -- exactly
//     `SECRET_VALUE_MAX` is accepted, one more is refused -- and the ceiling
//     is the platform's own (2048 bytes, `expo-secure-store` on Android);
//   * printable ASCII only, so `${'a\nb'}`-style escapes cannot be built;
//   * the constructor throws, so a `Secret` in a bad shape cannot exist even
//     for a caller that went around the refusing factory.

import assert from 'node:assert/strict';
import { inspect } from 'node:util';
import test from 'node:test';

import { SECRET_CODES, SECRET_ITEMS, SECRET_VALUE_MAX, Secret, isSecret, isSecretItem, secret } from './secret.js';

const ID_TOKEN = 'synthetic-id-token';
const MAC_KEY = 'synthetic-mac-key';

test('SECRET_ITEMS is D111\'s list, and it is frozen', () => {
  assert.deepEqual([...SECRET_ITEMS], ['idToken', 'macKey']);
  assert.equal(Object.isFrozen(SECRET_ITEMS), true);
});

test('isSecretItem knows the list and nothing else', () => {
  assert.equal(isSecretItem('idToken'), true);
  assert.equal(isSecretItem('macKey'), true);
  assert.equal(isSecretItem('password'), false);
  assert.equal(isSecretItem(''), false);
  assert.equal(isSecretItem(undefined), false);
});

test('a well-formed pair is accepted and carried verbatim', () => {
  const result = secret('idToken', ID_TOKEN);
  assert.equal(result.ok, true);
  assert.equal(result.secret.item, 'idToken');
  assert.equal(result.secret.reveal(), ID_TOKEN);
  assert.equal(isSecret(result.secret), true);
  assert.equal(Object.isFrozen(result.secret), true);
});

test('the refusals: a bad item, a bad type, an empty, a printable-ASCII breach', () => {
  assert.equal(secret('password', ID_TOKEN).code, SECRET_CODES.BAD_ITEM);
  assert.equal(secret(undefined, ID_TOKEN).code, SECRET_CODES.BAD_ITEM);
  assert.equal(secret('idToken', 7).code, SECRET_CODES.BAD_TYPE);
  assert.equal(secret('idToken', null).code, SECRET_CODES.BAD_TYPE);
  assert.equal(secret('idToken', '').code, SECRET_CODES.EMPTY);
  for (const value of ['has space', 'has\ttab', 'has\nnewline', 'café', 'emoji🙂']) {
    const result = secret('idToken', value);
    assert.equal(result.code, SECRET_CODES.NOT_PRINTABLE, JSON.stringify(value));
    assert.equal(result.field, 'value');
  }
});

test('the length ceiling is a boundary on both sides, in bytes', () => {
  const longest = 'x'.repeat(SECRET_VALUE_MAX);
  assert.equal(secret('macKey', longest).ok, true);
  assert.equal(
    secret('macKey', 'x'.repeat(SECRET_VALUE_MAX + 1)).code,
    SECRET_CODES.OVERSIZE,
  );
  // Nothing is truncated on the way in: the accepted value is the whole value.
  assert.equal(secret('macKey', longest).secret.reveal().length, SECRET_VALUE_MAX);
});

test('printing a secret prints the placeholder, in every conversion', () => {
  const s = secret('idToken', ID_TOKEN).secret;
  const printed = [
    String(s),
    `${s}`,
    s + '',
    JSON.stringify(s),
    JSON.stringify({ token: s }),
    JSON.stringify([s]),
    inspect(s),
    inspect({ token: s }),
    JSON.stringify(Object.entries(s)),
    JSON.stringify({ ...s }),
    String(+s),
    `${[s]}`,
  ];
  for (const text of printed) {
    assert.equal(text.includes(ID_TOKEN), false, text);
  }
  assert.equal(String(s), '[secret idToken]');
  assert.equal(String(secret('macKey', MAC_KEY).secret), '[secret macKey]');

  // ... and each print method BY NAME. This is not belt-and-braces: a
  // conversion (`String(s)`, `s + ''`, a template) never reaches `toString`,
  // because `Symbol.toPrimitive` is consulted first and short-circuits it. So
  // the list above pins `Symbol.toPrimitive`, and this loop is the only thing
  // that pins `toString` and `toJSON` at all -- without it a `toString` that
  // returned the material would pass every assertion here.
  const direct = [
    s.toString(),
    s.toJSON(),
    s[Symbol.toPrimitive]('string'),
    s[Symbol.toPrimitive]('number'),
    s[Symbol.for('nodejs.util.inspect.custom')](),
  ];
  for (const text of direct) {
    assert.equal(text.includes(ID_TOKEN), false, text);
    assert.equal(text, '[secret idToken]');
  }
});

test('the positive control: a plain string DOES print, so the test can fail', () => {
  // If the check above could not catch a leak, a green run would mean nothing.
  assert.equal(`${ID_TOKEN}`.includes(ID_TOKEN), true);
  assert.equal(JSON.stringify({ token: ID_TOKEN }).includes(ID_TOKEN), true);
  assert.equal(inspect({ token: ID_TOKEN }).includes(ID_TOKEN), true);
});

test('enumerating a secret finds no value: the material is in a private field', () => {
  const s = secret('macKey', MAC_KEY).secret;
  assert.deepEqual(Object.keys(s), []);
  assert.deepEqual(Object.entries(s), []);
  assert.deepEqual({ ...s }, {});
  assert.equal(Object.getOwnPropertyNames(s).includes('#value'), false);
  // The one way to it is the named one.
  assert.equal(s.reveal(), MAC_KEY);
});

test('isSecret does not confuse a look-alike for a secret', () => {
  const lookAlike = { item: 'idToken', reveal: () => ID_TOKEN };
  assert.equal(isSecret(lookAlike), false);
  assert.equal(isSecret(ID_TOKEN), false);
  assert.equal(isSecret(null), false);
});

test('the constructor throws, so a Secret in a bad shape cannot exist', () => {
  assert.throws(() => new Secret('password', ID_TOKEN), TypeError);
  assert.throws(() => new Secret('idToken', ''), TypeError);
  assert.throws(() => new Secret('idToken', 7), TypeError);
});
