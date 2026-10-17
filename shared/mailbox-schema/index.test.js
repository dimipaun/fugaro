// The message schema's test (mobile plan, Task P0.2).
//
// Written BEFORE the module it imports, on purpose — the same red step P0.1
// took with `shared/skeleton/`: `npm test` from `shared/` discovers this file
// through the manifest's glob and fails with ERR_MODULE_NOT_FOUND until
// `shared/mailbox-schema/index.js` exists. A green run over a directory the
// glob never reached would prove nothing (shared/README.md says why).
//
// The schema is the protocol (plan P0.2): what P1.1's state machine validates
// against, what P3.6's client writes and what P4.1's consumer reads. Its rule
// is the plan's rule — **fail closed**: an unknown field, a wrong type, an
// oversize body and every other ambiguity is a REFUSAL with its own code,
// never a coercion. So the cases below are mostly refusals, and each one is
// grouped by the code it must carry, because a validator that refuses
// everything with one code cannot be told apart from one that refuses nothing
// it means to.
//
// What this file deliberately does NOT test, because it is not this task's:
// the redactor (P1.3 — text is redacted before it is validated, and the case
// near the end pins that boundary rather than assuming it), the replay window
// and the dedupe table (P1.1), the URL rules (P0.3) and the ack's own rules
// (P2.1). The schema decides one thing: whether a value is a message.
//
// No network, no clock, no filesystem, no randomness, no dependency: node's
// own test runner and assert, over plain values.

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  AT_MAX,
  ID_MAX,
  REFUSALS,
  SENDER_MAX,
  TEXT_MAX,
  isMessage,
  validateMessage,
} from './index.js';

// good returns a fresh well-formed message; overrides are merged in so a case
// can break exactly one thing.
function good(over = {}) {
  return {
    id: 'm-0001',
    sender: 'phone',
    text: 'run 42 finished',
    at: 1_700_000_000_000,
    ...over,
  };
}

// without returns a copy of a well-formed message with one field removed.
function without(field) {
  const value = good();
  delete value[field];
  return value;
}

// refusal asserts the verdict for value is a refusal carrying code, and that
// no message came with it.
function refusal(value, code, field) {
  const verdict = validateMessage(value);
  assert.equal(verdict.ok, false, `expected a refusal for ${JSON.stringify(value)}`);
  assert.equal(verdict.code, code, `code for ${JSON.stringify(value)}`);
  assert.equal(verdict.message, undefined, 'a refusal carries no message');
  if (field !== undefined) {
    assert.equal(verdict.field, field, `field for ${JSON.stringify(value)}`);
  }
  return verdict;
}

test('a well-formed message is accepted, and comes back frozen', () => {
  const verdict = validateMessage(good());
  assert.equal(verdict.ok, true);
  assert.deepEqual(verdict.message, good());
  assert.equal(Object.isFrozen(verdict.message), true, 'a validated message is frozen');
});

test('the accepted message is a copy, not the caller\'s object', () => {
  const raw = good();
  const { message } = validateMessage(raw);
  assert.notEqual(message, raw);
  assert.equal(Object.getPrototypeOf(message), Object.prototype, 'a plain, unpolluted object');
  // And the schema never coerces: what the caller wrote is what the consumer reads.
  const spaced = validateMessage(good({ text: '  padded  ', sender: ' phone ' }));
  assert.equal(spaced.message.text, '  padded  ', 'text is not trimmed');
  assert.equal(spaced.message.sender, ' phone ', 'sender is not trimmed');
});

test('an optional ack is accepted, present or absent, and absent means not acked', () => {
  assert.equal(validateMessage(good({ acked: true })).ok, true);
  assert.equal(validateMessage(good({ acked: false })).ok, true);
  const { message } = validateMessage(good());
  assert.equal(Object.hasOwn(message, 'acked'), false, 'an absent ack adds no field');
});

test('a value that is not an object is refused', () => {
  for (const value of [null, undefined, 7, 'message', true, [], [good()], () => {}]) {
    refusal(value, REFUSALS.NOT_AN_OBJECT);
  }
});

test('a missing required field is refused with the field named', () => {
  for (const field of ['id', 'sender', 'text', 'at']) {
    const value = good();
    delete value[field];
    refusal(value, REFUSALS.MISSING_FIELD, field);
  }
});

test('an unknown field is refused, so the tree never stores what it did not validate', () => {
  refusal(good({ extra: 'anything' }), REFUSALS.UNKNOWN_FIELD, 'extra');
  refusal(good({ Acked: true }), REFUSALS.UNKNOWN_FIELD, 'Acked'); // the field names are exact
  refusal(good({ Text: 'x' }), REFUSALS.UNKNOWN_FIELD, 'Text');
  // A key arriving from JSON, where __proto__ is an own property: the schema
  // names its fields, so it is an unknown field, not pollution.
  const smuggled = JSON.parse(
    `{"id":"m-0001","sender":"phone","text":"hi","at":1,"__proto__":{"polluted":true}}`,
  );
  refusal(smuggled, REFUSALS.UNKNOWN_FIELD, '__proto__');
  assert.equal({}.polluted, undefined, 'nothing was polluted');
});

test('a wrong type is refused, never coerced', () => {
  refusal(good({ id: 1 }), REFUSALS.WRONG_TYPE, 'id');
  refusal(good({ id: {} }), REFUSALS.WRONG_TYPE, 'id');
  refusal(good({ sender: ['phone'] }), REFUSALS.WRONG_TYPE, 'sender');
  refusal(good({ text: null }), REFUSALS.WRONG_TYPE, 'text');
  refusal(good({ at: '1700000000000' }), REFUSALS.WRONG_TYPE, 'at');
  refusal(good({ acked: 'true' }), REFUSALS.WRONG_TYPE, 'acked');
  refusal(good({ acked: 0 }), REFUSALS.WRONG_TYPE, 'acked');
  // Present-but-undefined is its own ambiguity, and it is a refusal too: a
  // field that exists must hold a value.
  refusal(good({ id: undefined }), REFUSALS.WRONG_TYPE, 'id');
  refusal(good({ acked: undefined }), REFUSALS.WRONG_TYPE, 'acked');
});

test('an id is a database key: the tree\'s own forbidden characters are refused', () => {
  // The same predicate the CLI's budget.Key already applies (internal/budget),
  // because a message id is a node key under the mailbox the rules grant.
  for (const char of ['%', '.', '$', '#', '[', ']', '/']) {
    refusal(good({ id: `m${char}1` }), REFUSALS.BAD_KEY, 'id');
    refusal(good({ id: char }), REFUSALS.BAD_KEY, 'id');
  }
  refusal(good({ id: 'm\u00001' }), REFUSALS.BAD_KEY, 'id'); // a control character
  refusal(good({ id: 'm\u001F1' }), REFUSALS.BAD_KEY, 'id');
  refusal(good({ id: 'm\u007F1' }), REFUSALS.BAD_KEY, 'id');
  // The near misses the tree's other keys also refuse as characters: these are
  // ordinary characters and stay legal, so the rule is a character rule and
  // not a "reject anything unusual" rule.
  for (const id of ['m-0001', 'm_0001', 'M0001', '0', 'a'.repeat(ID_MAX)]) {
    assert.equal(validateMessage(good({ id })).ok, true, `id ${JSON.stringify(id)} is accepted`);
  }
});

test('an id is non-empty and bounded', () => {
  refusal(good({ id: '' }), REFUSALS.EMPTY_STRING, 'id');
  refusal(good({ id: 'a'.repeat(ID_MAX + 1) }), REFUSALS.TOO_LONG, 'id');
});

test('a sender is non-empty, bounded and a single line', () => {
  refusal(good({ sender: '' }), REFUSALS.EMPTY_STRING, 'sender');
  refusal(good({ sender: 'a'.repeat(SENDER_MAX + 1) }), REFUSALS.TOO_LONG, 'sender');
  refusal(good({ sender: 'phone\n' }), REFUSALS.BAD_CHARACTER, 'sender');
  refusal(good({ sender: 'phone\u0000' }), REFUSALS.BAD_CHARACTER, 'sender');
  assert.equal(validateMessage(good({ sender: 'a'.repeat(SENDER_MAX) })).ok, true);
  // A sender is a value, not a path segment: the database-key rule is the id's,
  // so an identity that carries a dot or an @ is still a sender.
  assert.equal(validateMessage(good({ sender: 'owner@example.com' })).ok, true);
});

test('the body is non-empty and carries the size bound', () => {
  refusal(good({ text: '' }), REFUSALS.EMPTY_STRING, 'text');
  refusal(good({ text: 'x'.repeat(TEXT_MAX + 1) }), REFUSALS.TOO_LONG, 'text');
  assert.equal(validateMessage(good({ text: 'x'.repeat(TEXT_MAX) })).ok, true, 'the bound itself');
  // A body is speech: it may be several lines, and a line break is not an
  // error the phone should have to strip before writing.
  assert.equal(validateMessage(good({ text: 'one\ntwo\tthree' })).ok, true);
  refusal(good({ text: 'one\u0000two' }), REFUSALS.BAD_CHARACTER, 'text');
  refusal(good({ text: 'one\u007Ftwo' }), REFUSALS.BAD_CHARACTER, 'text');
});

test('a string that cannot survive JSON is refused', () => {
  // An unpaired surrogate is not a character: it encodes to broken JSON, so a
  // value carrying one is not a message that can be written at all.
  const lone = '\uD800';
  refusal(good({ id: `m${lone}` }), REFUSALS.BAD_KEY, 'id');
  refusal(good({ sender: `p${lone}` }), REFUSALS.BAD_CHARACTER, 'sender');
  refusal(good({ text: `x${lone}y` }), REFUSALS.BAD_CHARACTER, 'text');
  assert.equal(validateMessage(good({ text: 'x😀y' })).ok, true, 'a paired surrogate is fine');
});

test('a timestamp is an epoch-millisecond safe integer, and nothing else', () => {
  assert.equal(validateMessage(good({ at: 0 })).ok, true);
  assert.equal(validateMessage(good({ at: AT_MAX })).ok, true);
  for (const at of [-1, -0.5, 1.5, NaN, Infinity, -Infinity, AT_MAX + 1, 2 ** 53]) {
    refusal(good({ at }), REFUSALS.BAD_TIMESTAMP, 'at');
  }
});

test('every refusal family has its own code', () => {
  // The codes are the contract P1.1, P3.6 and P4.1 branch on: two families
  // sharing one code would make "refused" indistinguishable from "refused for
  // the other reason", which is what a fail-closed schema exists to avoid.
  const codes = [
    refusal(null, REFUSALS.NOT_AN_OBJECT).code,
    refusal(good({ extra: 1 }), REFUSALS.UNKNOWN_FIELD).code,
    refusal(without('id'), REFUSALS.MISSING_FIELD).code,
    refusal(good({ id: 1 }), REFUSALS.WRONG_TYPE).code,
    refusal(good({ id: '' }), REFUSALS.EMPTY_STRING).code,
    refusal(good({ id: 'a.b' }), REFUSALS.BAD_KEY).code,
    refusal(good({ id: 'a'.repeat(ID_MAX + 1) }), REFUSALS.TOO_LONG).code,
    refusal(good({ text: '\u0000' }), REFUSALS.BAD_CHARACTER).code,
    refusal(good({ at: 1.5 }), REFUSALS.BAD_TIMESTAMP).code,
  ];
  assert.equal(new Set(codes).size, codes.length, `codes are distinct: ${codes.join(', ')}`);
  assert.deepEqual(new Set(codes), new Set(Object.values(REFUSALS)), 'and no code is unused');
  for (const code of codes) {
    assert.equal(typeof code, 'string');
    assert.notEqual(code, '', 'every refusal is named');
  }
});

test('isMessage agrees with validateMessage', () => {
  assert.equal(isMessage(good()), true);
  assert.equal(isMessage(good({ acked: true })), true);
  assert.equal(isMessage(good({ id: '' })), false);
  assert.equal(isMessage(null), false);
  assert.equal(isMessage(good({ extra: 1 })), false);
});

test('the schema is not the redactor: redaction happens before it (P1.3)', () => {
  // P0.2 is on the plan's critical path and is deliberately SMALL: the
  // redactor is P1.3's, and D118 redacts before the write, so by the time a
  // value reaches this schema it has already been through it. This case pins
  // that boundary — the schema neither redacts nor refuses a token-shaped
  // body, it only bounds it — so that a later task cannot quietly grow a
  // second, weaker redactor in here.
  const tokenShaped = ['ghp', 'x'.repeat(36)].join('_'); // built at run time: no secret-shaped literal is committed
  const verdict = validateMessage(good({ text: `the token is ${tokenShaped}` }));
  assert.equal(verdict.ok, true);
  assert.equal(verdict.message.text, `the token is ${tokenShaped}`, 'returned verbatim, unredacted');
});
