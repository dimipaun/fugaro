// The installation tuple's tests (mobile plan, Task P3.1; decision D114).
//
// D114 makes an account an **installation** -- one Firebase project, one
// operator identity, one Fugaro project name -- and this file pins what that
// tuple is and is not:
//
//   * a well-formed tuple is ACCEPTED, comes back frozen, and carries the
//     caller's values verbatim;
//   * EVERY refusal has a code and (where one is at fault) a field, and
//     nothing is coerced, trimmed or repaired on the way in;
//   * the id is a **storage key segment** -- it namespaces the secure-storage
//     item and the cache -- so its alphabet and its ceiling are tested as
//     hard boundaries, on both sides;
//   * the other three fields are text, not keys, and a name that is merely
//     **not ASCII** is accepted (the positive control), so the rule cannot
//     quietly become "ASCII only";
//   * `sameInstallation` compares the TRIPLE the store refuses duplicates on,
//     and not the id the owner chose for it.
//
// No value here is a credential and no literal is secret-shaped: a Firebase
// project id is an address, an Auth uid is an identity, and both are built
// from harmless pieces rather than written as anything a scanner could
// mistake for a secret.

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  ACCOUNT_CODES,
  ACCOUNT_FIELDS,
  ACCOUNT_ID_MAX,
  ACCOUNT_TEXT_MAX,
  isAccountId,
  sameInstallation,
  validateAccount,
} from './account.js';

/** A well-formed tuple, built fresh so no test can mutate another's. */
function tuple(over = {}) {
  return {
    id: 'laptop',
    firebaseProjectId: 'fugaro-dev',
    authUid: 'uid-0001',
    fugaroProject: 'fugaro',
    ...over,
  };
}

test('a well-formed tuple is accepted, frozen, and carries exactly the fields', () => {
  const result = validateAccount(tuple());
  assert.equal(result.ok, true);
  assert.deepEqual(Object.keys(result.account), [...ACCOUNT_FIELDS]);
  assert.equal(Object.isFrozen(result.account), true);
  assert.equal(result.account.id, 'laptop');
  assert.equal(result.account.fugaroProject, 'fugaro');
});

test('the accepted copy is a copy: the caller\'s object is not handed back', () => {
  const input = tuple();
  const result = validateAccount(input);
  assert.notEqual(result.account, input);
});

test('an object that is not a plain object is refused before anything is read', () => {
  for (const value of [null, undefined, 'laptop', 7, true, [], ['laptop']]) {
    assert.equal(validateAccount(value).code, ACCOUNT_CODES.NOT_AN_OBJECT);
  }
  class Installation {}
  assert.equal(validateAccount(new Installation()).code, ACCOUNT_CODES.NOT_AN_OBJECT);
});

test('a missing field is refused, and the refusal names it', () => {
  for (const field of ACCOUNT_FIELDS) {
    const partial = tuple();
    delete partial[field];
    const result = validateAccount(partial);
    assert.equal(result.ok, false);
    assert.equal(result.code, ACCOUNT_CODES.MISSING_FIELD);
    assert.equal(result.field, field);
  }
});

test('a field this module does not define is refused rather than dropped', () => {
  const result = validateAccount(tuple({ notes: 'a field the store would silently drop' }));
  assert.equal(result.code, ACCOUNT_CODES.UNKNOWN_FIELD);
  assert.equal(result.field, 'notes');
});

test('every refusal carries a code, and the field it is about', () => {
  const cases = [
    [tuple({ id: 7 }), ACCOUNT_CODES.BAD_TYPE],
    [tuple({ firebaseProjectId: null }), ACCOUNT_CODES.BAD_TYPE],
    [tuple({ id: '' }), ACCOUNT_CODES.EMPTY],
    [tuple({ authUid: '' }), ACCOUNT_CODES.EMPTY],
    [tuple({ id: ' laptop' }), ACCOUNT_CODES.PADDED],
    [tuple({ fugaroProject: 'fugaro ' }), ACCOUNT_CODES.PADDED],
    [tuple({ id: 'laptop/../other' }), ACCOUNT_CODES.BAD_ID],
    [tuple({ id: 'a.b' }), ACCOUNT_CODES.BAD_ID],
    [tuple({ id: 'the laptop' }), ACCOUNT_CODES.BAD_ID],
    [tuple({ id: 'lápiz' }), ACCOUNT_CODES.BAD_ID],
    [tuple({ authUid: 'uid 0001' }), ACCOUNT_CODES.UNSAFE_TEXT],
  ];
  for (const [value, code] of cases) {
    const result = validateAccount(value);
    assert.equal(result.code, code, `expected ${code} for ${JSON.stringify(value)}`);
    assert.equal(typeof result.field, 'string');
  }
});

test('the id is a key segment: its ceiling is a boundary, on both sides', () => {
  const longest = 'a'.repeat(ACCOUNT_ID_MAX);
  assert.equal(validateAccount(tuple({ id: longest })).ok, true);
  assert.equal(
    validateAccount(tuple({ id: 'a'.repeat(ACCOUNT_ID_MAX + 1) })).code,
    ACCOUNT_CODES.OVERSIZE,
  );
});

test('the text fields are measured in code points, not UTF-16 units', () => {
  // Each emoji is one code point and two code units.
  const exact = '🙂'.repeat(ACCOUNT_TEXT_MAX);
  assert.equal([...exact].length, ACCOUNT_TEXT_MAX);
  assert.ok(exact.length > ACCOUNT_TEXT_MAX);
  assert.equal(validateAccount(tuple({ fugaroProject: exact })).ok, true);
  assert.equal(
    validateAccount(tuple({ fugaroProject: '🙂'.repeat(ACCOUNT_TEXT_MAX + 1) })).code,
    ACCOUNT_CODES.OVERSIZE,
  );
});

test('a value that is not already NFC is refused rather than normalized', () => {
  // "é" decomposed: normalizing would quietly make two inputs one identity.
  const decomposed = 'café';
  assert.notEqual(decomposed, decomposed.normalize('NFC'));
  assert.equal(
    validateAccount(tuple({ fugaroProject: decomposed })).code,
    ACCOUNT_CODES.NOT_NFC,
  );
  // ... and the same name in NFC is accepted, so the rule is not a tax on
  // names that are merely non-ASCII.
  assert.equal(validateAccount(tuple({ fugaroProject: 'café' })).ok, true);
});

test('unprintable and look-alike-blank characters are refused', () => {
  // Each character sits BETWEEN two letters, so the category under test is
  // what fires: the same character at either end is caught one rule earlier,
  // as `padded`, which is asserted beside this loop.
  const cases = {
    'C0 control': 'fugaro\u0007dev',
    'C1 control': 'fugaro\u0085dev',
    'zero-width space (Cf)': 'fugaro​dev',
    'right-to-left override (Cf)': 'fugaro‮dev',
    'line separator (Zl)': 'fugaro dev',
    'paragraph separator (Zp)': 'fugaro dev',
    'braille blank (looks empty)': 'fugaro⠀dev',
    'space (Zs)': 'fugaro dev',
  };
  for (const [what, value] of Object.entries(cases)) {
    const result = validateAccount(tuple({ fugaroProject: value }));
    assert.equal(result.code, ACCOUNT_CODES.UNSAFE_TEXT, what);
    assert.equal(result.field, 'fugaroProject', what);
  }
  // The order of the rules, asserted: at either end the same characters are
  // `padded`, which is checked first -- so a value carrying one is refused for
  // a named reason and not by accident.
  for (const value of ['fugaro ', 'fugaro ', 'fugaro\t']) {
    assert.equal(
      validateAccount(tuple({ fugaroProject: value })).code,
      ACCOUNT_CODES.PADDED,
      JSON.stringify(value),
    );
  }
});

test('a tuple is owner-typed, so a name that is merely not ASCII is accepted', () => {
  const result = validateAccount(
    tuple({ fugaroProject: 'プロジェクト', firebaseProjectId: 'fugaro-prüfung' }),
  );
  assert.equal(result.ok, true, 'the rule must not quietly become "ASCII only"');
  assert.equal(result.account.fugaroProject, 'プロジェクト');
});

test('sameInstallation compares D114\'s triple, not the owner\'s id', () => {
  const a = validateAccount(tuple()).account;
  assert.equal(sameInstallation(a, validateAccount(tuple({ id: 'phone' })).account), true);
  for (const field of ['firebaseProjectId', 'authUid', 'fugaroProject']) {
    const other = validateAccount(tuple({ id: 'phone', [field]: 'other' })).account;
    assert.equal(sameInstallation(a, other), false, field);
  }
});

test('isAccountId is the predicate the key composers share', () => {
  assert.equal(isAccountId('laptop'), true);
  assert.equal(isAccountId('a_b-1'), true);
  assert.equal(isAccountId(''), false);
  assert.equal(isAccountId('a.b'), false);
  assert.equal(isAccountId('a'.repeat(ACCOUNT_ID_MAX + 1)), false);
  assert.equal(isAccountId(7), false);
});
