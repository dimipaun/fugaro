// From a transcript to a message: the rule's own tests (Task P3.4).
//
// The rule is one line of the plan's verification -- *a fake transcript
// becomes a message* -- and the interesting half of it is that **this module
// decides almost nothing**. The schema (P0.2) is the protocol: it accepts or
// refuses, and every refusal here is the schema's own code passed through
// unchanged. These tests assert that, so that a later change cannot quietly
// grow a second, looser rule in `src/audio/` -- two artifacts disagreeing about
// what a message is is the failure D119 and the schema exist to prevent.

import assert from 'node:assert/strict';
import test from 'node:test';

import { FIELDS, REQUIRED_FIELDS, validateMessage } from '../../../shared/mailbox-schema/index.js';
import { DRAFT_FIELDS, messageDraft } from './message.js';

/** A draft that is right in every field, so each test can move one thing. */
function draft(overrides = {}) {
  return { id: 'm-1', sender: 'phone-installation-a', text: 'running the tests', at: 1_700_000_000_000, ...overrides };
}

test('the draft supplies exactly the schema\'s required fields, less the mac the client signs', () => {
  assert.deepEqual(
    [...DRAFT_FIELDS],
    [...REQUIRED_FIELDS].filter((field) => field !== 'mac'),
    'the draft and the protocol cannot disagree',
  );
});

test('a final transcript becomes a message the shared schema accepts', () => {
  const verdict = messageDraft(draft());
  assert.equal(verdict.ok, true);
  const signed = { ...verdict.message, mac: '0'.repeat(64) };
  assert.equal(validateMessage(signed).ok, true, 'the draft plus a well-formed tag is a message the schema accepts');
  assert.equal(Object.isFrozen(verdict.message), true);
  assert.deepEqual(
    Object.keys(verdict.message).sort(),
    [...DRAFT_FIELDS].sort(),
    'a draft has no field of its own: not `mac` (P3.6 signs it), not `acked` (the consumer\'s)',
  );
  assert.equal(verdict.message.text, 'running the tests');
  assert.equal(verdict.message.at, 1_700_000_000_000);
});

test('nothing is repaired: the schema\'s refusal is passed through as itself', () => {
  // The case the microphone actually produces: a cough, and an empty string.
  assert.deepEqual({ ...messageDraft(draft({ text: '' })) }, { ok: false, code: 'empty-string', field: 'text' });
  assert.deepEqual({ ...messageDraft(draft({ sender: 42 })) }, { ok: false, code: 'wrong-type', field: 'sender' });
  assert.deepEqual({ ...messageDraft(draft({ at: 1.5 })) }, { ok: false, code: 'bad-timestamp', field: 'at' });
  assert.deepEqual({ ...messageDraft(draft({ at: -1 })) }, { ok: false, code: 'bad-timestamp', field: 'at' });
  assert.deepEqual({ ...messageDraft(draft({ id: 'no/slashes' })) }, { ok: false, code: 'bad-key', field: 'id' });
  const { id, ...withoutId } = draft();
  assert.deepEqual({ ...messageDraft(withoutId) }, { ok: false, code: 'missing-field', field: 'id' });
  assert.deepEqual({ ...messageDraft(draft({ text: 'x'.repeat(4097) })) }, { ok: false, code: 'too-long', field: 'text' });
});

test('a field this draft does not have is refused with the protocol\'s own code', () => {
  // P6.3's `kind` is proposed and NOT APPROVED, and a draft that accepted it
  // would be a draft that silently dropped it -- or, worse, one that carried a
  // field the rules deny (the message node closes `$other`).
  assert.deepEqual({ ...messageDraft(draft({ kind: 'command' })) }, { ok: false, code: 'unknown-field', field: 'kind' });
  assert.deepEqual({ ...messageDraft(draft({ mac: 'deadbeef' })) }, { ok: false, code: 'unknown-field', field: 'mac' });
});

test('a draft that is not an object is refused, and never coerced', () => {
  for (const value of [null, undefined, 42, 'a message', [], [draft()]]) {
    const verdict = messageDraft(value);
    assert.equal(verdict.ok, false);
    assert.equal(verdict.code, 'not-an-object', String(value));
  }
});
