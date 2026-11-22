// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// What the app says about push: its tests (mobile plan, Task P5.2.8; the
// design's §3.1 and §3.8).
//
// Two properties, and both are the design's rather than a screen's taste:
//
//   * **a phrase the app composed names push and nothing else.** No repository,
//     no run, no amount, no id, no code, no token -- §3.1's rule, which §4
//     rules 1 and 3 turn into "a push surface cannot be turned into a lure".
//     The test walks the rendered text and refuses a token-shaped or
//     repository-shaped string in it.
//   * **an unrecognised refusal is the waiting phrase, and never its code.**
//     A code is a word for the app; a screen that printed one would be showing
//     the owner a sentence nobody wrote.

import assert from 'node:assert/strict';
import test from 'node:test';

import { CONTROL_DEFAULTS, CONTROL_NAMES } from './controls.js';
import { RELAY_CODES } from './relay.js';
import { PUSH_STATES } from './registration.js';
import { CONTROL_LINES, PUSH_LINES, PUSH_WAITING, pushScreen, pushStateLine } from './screen.js';
import { TOKEN_CODES } from './token.js';

const TOKEN = 'not-a-real-token-0123456789';

test('the state line says which of the three states the phone is in', () => {
  assert.equal(pushStateLine({ state: PUSH_STATES.REGISTERED, code: null }), PUSH_LINES[PUSH_STATES.REGISTERED]);
  assert.equal(pushStateLine({ state: PUSH_STATES.NEVER, code: null }), PUSH_LINES[PUSH_STATES.NEVER]);
  // The three refusals a screen can do something with get a phrase of their own.
  // The keys of the table ARE the states and the codes, so a screen looks one
  // up and never has to know which vocabulary it was handed.
  assert.equal(pushStateLine({ state: PUSH_STATES.UNREGISTERED, code: TOKEN_CODES.NO_TOKEN }), PUSH_LINES[TOKEN_CODES.NO_TOKEN]);
  assert.equal(pushStateLine({ state: PUSH_STATES.UNREGISTERED, code: TOKEN_CODES.TOKEN_FAILED }), PUSH_LINES[TOKEN_CODES.TOKEN_FAILED]);
  assert.equal(pushStateLine({ state: PUSH_STATES.UNREGISTERED, code: RELAY_CODES.RELAY_FAILED }), PUSH_LINES[RELAY_CODES.RELAY_FAILED]);
  assert.equal(pushStateLine({ state: PUSH_STATES.UNREGISTERED, code: 'no-connection' }), PUSH_LINES['no-connection']);
  assert.equal(Object.keys(PUSH_LINES).length, 6, 'two states and four codes, and no phrase without a key');
});

test('a refusal this table does not recognise is the waiting phrase, and never its code', () => {
  const codes = ['store-failed', 'licence-lapsed', 'no-key', 'too-long', 'probe-refused', 'unknown-field', ''];
  for (const code of codes) {
    const line = pushStateLine({ state: PUSH_STATES.UNREGISTERED, code });
    assert.equal(line, PUSH_WAITING, code);
  }
  // ... and the same for anything that is not an outcome at all, so a screen
  // rendering a value it was handed early shows a phrase rather than `undefined`.
  for (const outcome of [undefined, null, 'registered', 42, {}, { state: 'something-else' }]) {
    const line = pushStateLine(outcome);
    assert.equal(typeof line, 'string');
    assert.notEqual(line, '');
    assert.equal(
      line === PUSH_LINES[PUSH_STATES.NEVER] || line === PUSH_WAITING,
      true,
      `${JSON.stringify(outcome)} -> ${line}`,
    );
  }
});

test('no phrase carries a token, an id, a repository or a code', () => {
  const rendered = [
    ...Object.values(PUSH_LINES),
    ...Object.values(CONTROL_LINES),
    pushScreen({ outcome: { state: PUSH_STATES.REGISTERED }, prefs: CONTROL_DEFAULTS }).state.text,
    pushScreen({ outcome: { state: PUSH_STATES.UNREGISTERED, code: TOKEN_CODES.NO_TOKEN } }).state.text,
    pushScreen({ prefs: { ...CONTROL_DEFAULTS, quiet_hours: { zone: 'Europe/Berlin', start: 1320, end: 420 } } })
      .quietHours.text,
  ];
  for (const text of rendered) {
    assert.equal(typeof text, 'string');
    assert.equal(text.includes(TOKEN), false, text);
    assert.equal(/[0-9a-f]{32,}/.test(text), false, `a hex run in: ${text}`);
    assert.equal(/\b[0-9a-f]{40,64}\b/.test(text), false, `a tag-shaped string in: ${text}`);
    // A code is the app's word for a branch; the phrases are English sentences.
    for (const code of ['no-token', 'token-failed', 'relay-failed', 'no-connection', 'store-failed', 'unknown-field']) {
      assert.equal(text.includes(code), false, `${code} in: ${text}`);
    }
    // A repository, a run or a pull request is never named (§3.1).
    assert.equal(/[a-z]+\/[a-z-]+/.test(text), false, `a repository-shaped name in: ${text}`);
  }
});

test('the five switches are rendered as §1’s own categories, and as they will travel', () => {
  const off = pushScreen({ prefs: { ...CONTROL_DEFAULTS, cap_near: false, prompt: false } });
  assert.deepEqual(off.controls.map((control) => control.name), [...CONTROL_NAMES]);
  assert.deepEqual(off.controls.map((control) => control.on), [true, true, true, false, false]);
  for (const control of off.controls) assert.equal(typeof control.text, 'string');
  assert.equal(off.controls[3].text, CONTROL_LINES.cap_near);
  // A screen that has not read a record yet shows the defaults rather than an
  // empty list: what the phone will send is what the owner never touched.
  assert.deepEqual(
    pushScreen().controls.map((control) => control.on),
    CONTROL_NAMES.map((name) => CONTROL_DEFAULTS[name]),
  );
  // A switch the design adds shows up as a name with no line yet, which is a
  // missing row a reader can see rather than a blank one.
  assert.equal(Object.keys(CONTROL_LINES).length, CONTROL_NAMES.length);
  for (const name of CONTROL_NAMES) assert.equal(typeof CONTROL_LINES[name], 'string', name);
});

test('quiet hours are a clock, a zone name, and off when there is no window', () => {
  const on = pushScreen({ prefs: { ...CONTROL_DEFAULTS, quiet_hours: { zone: 'Europe/Berlin', start: 1320, end: 420 } } });
  assert.equal(on.quietHours.on, true);
  assert.equal(on.quietHours.zone, 'Europe/Berlin');
  assert.equal(on.quietHours.text.includes('22:00'), true);
  assert.equal(on.quietHours.text.includes('07:00'), true);
  assert.equal(on.quietHours.text.includes('Europe/Berlin'), true);
  const off = pushScreen({ prefs: CONTROL_DEFAULTS });
  assert.equal(off.quietHours.on, false);
  assert.equal(typeof off.quietHours.text, 'string');
  assert.equal(Object.isFrozen(off.quietHours), true);
  assert.equal(Object.isFrozen(off.controls), true);
  assert.equal(Object.isFrozen(off), true);
  assert.equal(Object.isFrozen(off.state), true);
});
