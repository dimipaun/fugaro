// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The result blocks, held to what makes one pasteable into `app-spike-DRAFT.md`
// (task M0; Check 733).

import assert from 'node:assert/strict';
import test from 'node:test';

import { QUESTION_TITLES, VALUE_MAX, line, plain, resultBlock } from './format.js';

test('a value is flattened onto the one line it is written on', () => {
  // Firebase's refusals are multi-line and carry the service's own prose, and
  // the owner pastes a block into a markdown table row: a value that keeps its
  // newline moves the rest of the block into the cell below.
  assert.equal(plain('INVALID_IDP_RESPONSE :\n  the token\nis not valid'), 'INVALID_IDP_RESPONSE : the token is not valid');
  assert.equal(plain('  padded  '), 'padded');
  assert.equal(plain('tab\tseparated'), 'tab separated');
  // ... and a line built from one has exactly one line in it.
  assert.equal(line('Microsoft', 'a\nb').split('\n').length, 1);
});

test('an absent reading is UNVERIFIED, which is what the draft says', () => {
  // The draft's own word for "no reading yet", so a block pasted over an
  // UNVERIFIED cell never makes an unanswered question look answered.
  for (const value of [undefined, null, '', '   ', '\n']) {
    assert.equal(plain(value), 'UNVERIFIED', JSON.stringify(value));
  }
  assert.equal(plain(0), '0', 'a zero is a reading');
  assert.equal(plain(false), 'false', 'and so is a false');
});

test('a long value is cut, visibly, and stays one line', () => {
  const long = 'x'.repeat(VALUE_MAX * 2);
  const cut = plain(long);
  assert.ok(cut.length <= VALUE_MAX, `${cut.length} > ${VALUE_MAX}`);
  assert.match(cut, /\(cut\)$/, 'a cut value says so, so a truncated reading is not read as a whole one');
  // The mark lands inside the bound rather than beside it: the value is what a
  // clipboard block carries, and a block that overran VALUE_MAX would be a
  // bound that does not hold.
  assert.equal(plain('y'.repeat(100), { max: 20 }).length, 20);
});

test('a block names the check, the question and the phone', () => {
  const block = resultBlock({
    question: 'U4',
    phone: 'iPhone 15',
    fields: [
      ['Google sign-in', 'ACCEPTED (HTTP 200)'],
      ['Microsoft sign-in', 'REFUSED (HTTP 400): INVALID_IDP_RESPONSE'],
    ],
  });
  const lines = block.split('\n');
  assert.equal(lines[0], `Check 733 — ${QUESTION_TITLES.U4}`);
  assert.equal(lines[1], 'Phone: iPhone 15');
  assert.deepEqual(lines.slice(2), [
    '- Google sign-in: ACCEPTED (HTTP 200)',
    '- Microsoft sign-in: REFUSED (HTTP 400): INVALID_IDP_RESPONSE',
  ]);
});

test('a block with no fields is still a block, and a question nobody knows still names itself', () => {
  const block = resultBlock({ question: 'U9', phone: '', fields: [] });
  assert.deepEqual(block.split('\n'), ['Check 733 — U9', 'Phone: UNVERIFIED']);
});

test('a token handed to a block is a caller bug, and the block is not the guard', () => {
  // Stated as a test rather than left implied: this module flattens and bounds,
  // it does not redact. What keeps a token out of a block is that no parsed
  // answer here has a token field to hand over (`idp.test.js` asserts that) and
  // that no screen passes one -- so a caller that does pass one gets it copied,
  // which is why the screens are read for it.
  assert.equal(line('token', 'abc.def.ghi'), '- token: abc.def.ghi');
});
