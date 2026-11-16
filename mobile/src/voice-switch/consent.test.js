// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The consent list's rules: their tests (mobile plan, Task P5.3.6; D127; the
// design is docs/mobile/design/voice-evaluation-switch.md §4.3).
//
// The design's sentence this file pins is §4.3's: "An entry is a provider name,
// never a URL", one entry per named provider, and the date the user agreed. What
// it does **not** do is restate the shared pattern: every name, date and bound
// below is judged by asking `shared/voice-switch`, and each test asserts the
// code the shared module answers with, so a change to the provider pattern is a
// change in one place and these tests follow it.
//
// **No credential-shaped literal appears below.** The names are provider names
// and the dates are epoch milliseconds.

import assert from 'node:assert/strict';
import test from 'node:test';

import { MAX_CONSENT, PROVIDER_MAX, REFUSAL, indicator as decide } from '../../../shared/voice-switch/index.js';
import { CONSENT_FIELDS, CONSENT_MAX, hasConsent, withConsent, withoutConsent } from './consent.js';

const AT = 1700000000000;
const LATER = 1700000001000;
const NAME = 'deepgram';
const OTHER = 'assemblyai';

/** An entry, for a list handed in as if it were stored. */
const entry = (provider, at) => ({ provider, at });

test('an empty record has consented to nothing', () => {
  assert.equal(hasConsent([], NAME), false);
  assert.equal(hasConsent([], undefined), false);
  assert.equal(hasConsent([], null), false);
});

test('recording a consent keeps the name and the day the user agreed', () => {
  const agreed = withConsent([], NAME, AT);
  assert.equal(agreed.ok, true);
  assert.deepEqual(agreed.consent.map((item) => ({ ...item })), [entry(NAME, AT)]);
  assert.equal(hasConsent(agreed.consent, NAME), true);
  assert.equal(hasConsent(agreed.consent, OTHER), false, 'one name is not another');
  assert.ok(Object.isFrozen(agreed.consent), 'the list cannot be edited in place');
  assert.ok(Object.isFrozen(agreed.consent[0]), 'nor an entry');
});

test('a name agreed to twice keeps one entry, re-dated', () => {
  const first = withConsent([], NAME, AT);
  const second = withConsent(first.consent, NAME, LATER);
  assert.equal(second.ok, true);
  assert.equal(second.consent.length, 1, 'the design holds one entry per named provider');
  assert.equal(second.consent[0].at, LATER, 'the later agreement is the one in force');
});

test('two names are two entries, and only the recorded one is consented to', () => {
  const one = withConsent([], NAME, AT);
  const two = withConsent(one.consent, OTHER, LATER);
  assert.equal(two.ok, true);
  assert.equal(two.consent.length, 2);
  assert.deepEqual(two.consent.map((item) => item.provider).sort(), [NAME, OTHER].sort());
  assert.equal(hasConsent(two.consent, NAME), true);
  assert.equal(hasConsent(two.consent, OTHER), true);
  assert.equal(hasConsent(two.consent, 'somewhere.else'), false);
});

test('a name that is a URL is refused with the shared module\'s own code', () => {
  // §4.3: "a policy-carried URL is a policy-carried redirection". The rule is
  // the shared module's; this test asserts its code, not its pattern.
  for (const bad of ['https://provider.example', 'provider/example', 'a b', '', 'x'.repeat(PROVIDER_MAX + 1), 7, null]) {
    const refused = withConsent([], bad, AT);
    assert.equal(refused.ok, false, String(bad));
    assert.equal(refused.code, REFUSAL.BAD_PROVIDER, String(bad));
    // The field is the shared module's own path for the value it read, not a
    // spelling this module chose: the refusal a caller sees here is the one the
    // shared function gave.
    assert.equal(refused.field, decide({ direction: 'stt', consent: [entry(bad, AT)] }).field, String(bad));
    assert.equal(hasConsent([], bad), false, `${String(bad)} is not a name the app consented to`);
  }
});

test('a date that is not the day the user agreed is refused', () => {
  for (const bad of [0, -1, 1.5, 'yesterday', null, undefined, {}]) {
    const refused = withConsent([], NAME, bad);
    assert.equal(refused.ok, false, String(bad));
    assert.ok([REFUSAL.MISSING_FIELD, REFUSAL.INVALID_AT].includes(refused.code), `${String(bad)} answered ${refused.code}`);
  }
  assert.equal(withConsent([], NAME, AT).ok, true, 'a millisecond count is a date');
});

test('an epoch in seconds is a date to this rule too: the bound is on the number, not the unit', () => {
  // The shared reader accepts any integer in 1..TIME_MAX, and it cannot tell a
  // millisecond count from a second one. This is the module's honest limit as
  // much as the shared module's, and it is pinned so that a caller reading it
  // here does not have to discover it: what the app records must be
  // `Date.now()`, and a sheet that passes seconds records a 1970 date.
  assert.equal(withConsent([], NAME, Math.floor(AT / 1000)).ok, true);
});

test('a list this module cannot read is refused, not repaired', () => {
  const cases = [
    ['deepgram', REFUSAL.INVALID_CONSENT],
    [42, REFUSAL.INVALID_CONSENT],
    [[null], REFUSAL.NOT_AN_OBJECT],
    [[NAME], REFUSAL.NOT_AN_OBJECT],
    [[entry(NAME, AT), entry(NAME, LATER)], REFUSAL.DUPLICATE_CONSENT],
    [[{ ...entry(NAME, AT), until: LATER }], REFUSAL.UNKNOWN_FIELD],
    [[{ ...entry(NAME, AT), provider: 'https://provider.example' }], REFUSAL.BAD_PROVIDER],
  ];
  for (const [value, code] of cases) {
    const refused = withConsent(value, OTHER, AT);
    assert.equal(refused.ok, false, JSON.stringify(value));
    assert.equal(refused.code, code, JSON.stringify(value));
  }
});

test('an explicit null is the empty list, which is how the shared module reads it', () => {
  // `consent: null` is "no consent" to the module that judges the same value at
  // egress time, so it is edited here as the empty list rather than refused --
  // one reading of one value, and the second edit is the first one again.
  for (const empty of [null, undefined, []]) {
    const agreed = withConsent(empty, NAME, AT);
    assert.equal(agreed.ok, true, String(empty));
    assert.deepEqual(agreed.consent.map((item) => ({ ...item })), [entry(NAME, AT)], String(empty));
    const revoked = withoutConsent(empty, NAME);
    assert.equal(revoked.ok, true, String(empty));
    assert.deepEqual([...revoked.consent], [], String(empty));
  }
});

test('the list has a cap, and it is the shared module\'s cap', () => {
  assert.equal(CONSENT_MAX, MAX_CONSENT, 'the bound is borrowed, not restated');
  const full = Array.from({ length: MAX_CONSENT }, (unused, index) => entry(`provider${index}`, AT));
  assert.equal(withConsent(full, NAME, AT).ok, false, 'a full list takes no new name');
  assert.equal(withConsent(full.slice(0, MAX_CONSENT - 1), NAME, AT).ok, true, 'the last slot takes one');
});

test('the field set is the shared module\'s two fields', () => {
  assert.deepEqual([...CONSENT_FIELDS], ['provider', 'at']);
  // A third field is not "carried along": it is refused, which is what makes
  // this list the version of the record rather than a bag.
  const refused = withConsent([{ ...entry(NAME, AT), note: 'x' }], NAME, AT);
  assert.equal(refused.code, REFUSAL.UNKNOWN_FIELD);
});

test('revoking a name leaves the others, and revoking twice is the same as once', () => {
  const both = withConsent(withConsent([], NAME, AT).consent, OTHER, AT).consent;
  const revoked = withoutConsent(both, NAME);
  assert.equal(revoked.ok, true);
  assert.deepEqual(revoked.consent.map((item) => item.provider), [OTHER]);
  assert.equal(hasConsent(revoked.consent, NAME), false);
  const again = withoutConsent(revoked.consent, NAME);
  assert.equal(again.ok, true);
  assert.deepEqual(again.consent.map((item) => item.provider), [OTHER], 'a name the record does not hold is already revoked');
  const all = withoutConsent(revoked.consent, OTHER);
  assert.deepEqual([...all.consent], []);
});

test('revoking refuses a name that is not a name', () => {
  const refused = withoutConsent([entry(NAME, AT)], 'https://provider.example');
  assert.equal(refused.ok, false);
  assert.equal(refused.code, REFUSAL.BAD_PROVIDER);
});

test('revoking refuses a list it cannot read, rather than emptying it', () => {
  const refused = withoutConsent([entry(NAME, AT), entry(NAME, LATER)], NAME);
  assert.equal(refused.ok, false);
  assert.equal(refused.code, REFUSAL.DUPLICATE_CONSENT);
});

test('neither call mutates the list it was handed', () => {
  const before = [entry(NAME, AT), entry(OTHER, AT)];
  const snapshot = JSON.stringify(before);
  withConsent(before, NAME, LATER);
  withConsent(before, 'a-third-name', LATER);
  withoutConsent(before, NAME);
  assert.equal(JSON.stringify(before), snapshot, 'the caller\'s list is the caller\'s');
});
