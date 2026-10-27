// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The redactor's seam (P3.6; P1.3 owns the rule, D118 requires it).
//
// P3.6 adds three things to P1.3's redactor and nothing else, so this file
// asserts exactly those three and leaves the forms themselves -- the base64 and
// percent-escaped spellings, the longest-first ordering -- to P1.3's own tests,
// where they belong. What is added here:
//
//   * the secret list is validated **once, at construction**, so a client built
//     with a list that is not a list of strings fails where it is wired and not
//     at the first utterance;
//   * a redact that throws is a **refusal** (`redact-failed`), not a
//     pass-through: a message that was not redacted must not go out on a path
//     that then says it was;
//   * what comes back is a frozen verdict, so the caller cannot mistake "the
//     text was not redacted" for "the text is fine".
//
// The limits of redaction are P1.3's and are not restated as tests here: a
// secret shorter than the module's own floor is not a form, and a secret
// assembled out of two utterances is not caught by any string match. The first
// is asserted below because it is the one a caller of THIS function can trip
// over silently.

import assert from 'node:assert/strict';
import test from 'node:test';

import { outboundRedactor, REDACT_CODES } from './redact.js';

// Synthetic secret-shaped strings, from no real credential (Global Constraint
// 11). Each is longer than P1.3's four-byte floor.
const SECRET = 'synthetic-token-0123456789';
const OTHER = 'synthetic-passphrase-abcd';

test('a secret in the utterance is replaced before anything else sees the text', () => {
  const redact = outboundRedactor([SECRET, OTHER]);
  const said = redact(`deploy with ${SECRET} and ${OTHER}, please`);
  assert.equal(said.ok, true);
  assert.equal(said.text, 'deploy with [REDACTED] and [REDACTED], please');
  assert.equal(said.text.includes(SECRET), false);
  // Nothing to redact is not a refusal: the text goes through verbatim.
  assert.deepEqual(redact('run 42 finished'), { ok: true, text: 'run 42 finished' });
  // An empty list is a list of strings, and redacts nothing.
  assert.deepEqual(outboundRedactor([])('run 42'), { ok: true, text: 'run 42' });
});

test('the verdict is frozen, so a caller cannot edit it into a pass', () => {
  const redact = outboundRedactor([SECRET]);
  const said = redact(`say ${SECRET}`);
  assert.equal(Object.isFrozen(said), true);
  assert.throws(() => {
    'use strict';
    said.ok = false;
  }, TypeError);
});

test('the list is judged at construction, so a bad one cannot reach an utterance', () => {
  // P1.3's own error, raised while the client is being wired (the client's
  // constructor is what turns this into a TypeError naming no value).
  for (const secrets of [undefined, null, 'a-secret', 42, {}, [SECRET, 42], ['ok', null]]) {
    assert.throws(() => outboundRedactor(secrets), (error) => {
      assert.equal(error instanceof Error, true, String(secrets));
      assert.equal(JSON.stringify(error.message).includes(SECRET), false, 'the message echoes nothing');
      return true;
    }, String(secrets));
  }
});

test('a text that cannot be redacted is refused, and the refusal carries nothing', () => {
  // The only way left to make P1.3 throw, now that the list is validated: text
  // that is not text. `send` refuses such a text earlier, so this is the seam's
  // own backstop -- and it must be a refusal, because the alternative is
  // writing something that says "redacted" about a text nobody redacted.
  const redact = outboundRedactor([SECRET]);
  for (const text of [undefined, null, 42, {}, [SECRET], Symbol('x')]) {
    const refused = redact(text);
    assert.equal(refused.ok, false, String(text));
    assert.equal(refused.code, REDACT_CODES.REDACT_FAILED, String(text));
    assert.equal(refused.field, 'text');
    assert.equal(JSON.stringify(refused).includes(SECRET), false);
  }
});

test('a secret below the shared floor is not a form, and that is P1.3\'s rule', () => {
  // Not a bug to fix here: `redactForms` drops any form shorter than four
  // bytes, because a one-character replacement would mangle ordinary text. It
  // is asserted so that a reader of this module knows the boundary of what
  // "redacted" means on this path.
  const redact = outboundRedactor(['ab']);
  assert.deepEqual(redact('ab is short'), { ok: true, text: 'ab is short' });
});
