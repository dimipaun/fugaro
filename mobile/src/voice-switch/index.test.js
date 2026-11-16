// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The door's exported surface: its test (mobile plan, Task P5.3.6; D123, D127;
// `.claude/rules/mobile.md`: "Each module's `index.js` is its only entry point;
// import from it, never around it").
//
// The list below **is** the module's API, so a name added to it is a decision
// and this test fails until someone makes it: what a screen imports from here,
// P5.3.7's egress path will import from here, and the two files that compose a
// storage key or edit a stored record are deliberately not among them.

import assert from 'node:assert/strict';
import test from 'node:test';

import * as door from './index.js';

const EXPORTS = [
  'CONSENT_FIELDS',
  'CONSENT_MAX',
  'DEFAULT_VOICE_SETTINGS',
  'DIRECTIONS',
  'INDICATOR',
  'INDICATOR_FIELDS',
  'VOICE_CODES',
  'VOICE_RECORD_FIELDS',
  'VOICE_SETTINGS_PREFIX',
  'createVoiceSwitch',
  'failClosedIndicator',
  'hasConsent',
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
