// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The door's exported surface: its test (mobile plan, Tasks P5.3.6 and P5.3.7;
// D111, D118, D123, D127; `.claude/rules/mobile.md`: "Each module's `index.js`
// is its only entry point; import from it, never around it").
//
// The list below **is** the module's API, so a name added to it is a decision
// and this test fails until someone makes it: what a screen imports from here,
// P5.3.7's egress path imports from here too, and the files that compose a
// storage key or edit a stored record are deliberately not among them.
//
// **No host and no credential is on it either.** The door carries the hop's
// client and its two vocabularies (`HOP_CODES`, `HOP_REASON`), and never a URL:
// the address arrives as an argument and is checked against `shared/url`'s
// allow-list, whose default is empty until the owner names the host.

import assert from 'node:assert/strict';
import test from 'node:test';

import * as door from './index.js';

const EXPORTS = [
  'CONSENT_FIELDS',
  'CONSENT_MAX',
  'DEFAULT_VOICE_SETTINGS',
  'DESTRUCTIVE_VERBS',
  'DIRECTIONS',
  'EGRESS_CODE',
  'EGRESS_OFFER',
  'EGRESS_RULES',
  'HOP_CODES',
  'HOP_REASON',
  'INDICATOR',
  'INDICATOR_FIELDS',
  'REDACTED',
  'VOICE_CODES',
  'VOICE_RECORD_FIELDS',
  'VOICE_SETTINGS_PREFIX',
  'createVoiceEgress',
  'createVoiceHop',
  'createVoiceSwitch',
  'failClosedIndicator',
  'hasConsent',
  'permit',
  'readBackFor',
  'renderIndicator',
  'voiceSettingsKey',
  'withConsent',
  'withoutConsent',
];

test('the door exports exactly the surface a screen and the egress path need', () => {
  assert.deepEqual(Object.keys(door).sort(), [...EXPORTS].sort());
  for (const name of EXPORTS) {
    assert.notEqual(door[name], undefined, name);
  }
});

test('the two things a caller is meant to reach for are here', () => {
  assert.equal(typeof door.createVoiceSwitch, 'function', 'the switch, per account');
  assert.equal(typeof door.renderIndicator, 'function', 'the badge a screen draws');
});

test('the egress half is reachable, and carries no address of its own', () => {
  assert.equal(typeof door.createVoiceHop, 'function', 'the hop client, which the app builds');
  assert.equal(typeof door.createVoiceEgress, 'function', 'the ten rules, which the app asks');
  assert.equal(typeof door.permit, 'function', 'the rules as a pure question, for a caller that has no hop');
  assert.equal(typeof door.readBackFor, 'function', 'the read-back the local engine speaks');
  // §5's ten, named in the design's order: a rule that lost its name here would
  // still be a rule nobody could point at.
  assert.deepEqual([...door.EGRESS_RULES], [
    'confirmation-audio',
    'confirmation-pending',
    'destructive-route',
    'redaction-substitutes',
    'no-command',
    'foreign-project',
    'other-installation',
    'console-traffic',
    'no-credential',
    'no-spool',
  ]);
  // The redactor's two constants are `shared/digest`'s, carried rather than
  // re-spelled: D118 is one rule, and a door that gave it its own word would be
  // the second place the word could drift.
  assert.equal(door.REDACTED, '[REDACTED]');
  assert.deepEqual([...door.DESTRUCTIVE_VERBS], ['cancel', 'confirm', 'merge']);
  // No URL, and no host: the address is an argument, checked against the
  // allow-list, which ships empty until the owner names the hop (P5.3, item 2).
  for (const name of EXPORTS) {
    assert.equal(/url|host|base/i.test(name), false, name);
  }
  // A caller reaches the client's *answers*, not its wire: the paths and the
  // headers a request is made of stay on `hop.js`.
  for (const name of ['HOP_PATHS', 'HOP_HEADER', 'HOP_TEXT_TYPE', 'MAX_ANSWER_BYTES']) {
    assert.equal(EXPORTS.includes(name), false, name);
    assert.equal(Object.hasOwn(door, name), false, name);
  }
});

test('no file that composes a key or edits a record is exported', () => {
  // A screen that imported `settings.js` could compose a second key, and one
  // that imported `consent.js` could write a list the switch never judged.
  for (const name of ['readVoiceRecord', 'writeVoiceRecord', 'judgeConsent', 'judgeRecord', 'VoiceSwitch']) {
    assert.equal(EXPORTS.includes(name), false, name);
    assert.equal(Object.hasOwn(door, name), false, name);
  }
});

test('nothing named like a credential is on the door', () => {
  // Global Constraint 4 and the plan's own sentence: no provider key, and no
  // third credential item, is anywhere under `mobile/` -- least of all where a
  // screen or the egress path would reach for it.
  for (const name of EXPORTS) {
    assert.equal(/token|secret|credential|password/i.test(name), false, name);
  }
  assert.deepEqual([...door.DIRECTIONS], ['stt', 'tts']);
  assert.deepEqual({ ...door.DEFAULT_VOICE_SETTINGS }, { stt: 'local', tts: 'local' });
});
