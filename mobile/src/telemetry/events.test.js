// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The never-send test: what the app holds, and what a capture may hold
// (mobile plan, Task P7.3; D256, D255; the telemetry plan's §7.4, rule 2).
//
// **The claim, and why it is a test and not a sentence.** "No message, no
// mailbox and no account is ever sent" is D256's answer, and it is worth
// exactly as much as the fence behind it. The fence has two halves, and both
// are here: the module **refuses** a value that is not a boolean or a member of
// its closed set -- so the fixture's message text, mailbox key, account tuple
// and token cannot be sent even by a caller that tries -- and the test **holds
// the capture to the same question**, so a module that stopped refusing them
// would fail here even if it kept its own refusals quiet.
//
// **The fixtures are D118's and D114's own shapes** -- an account tuple, a
// mailbox message with a text and a tag, and a token -- because the values that
// may not leave are the values the app really holds, not a made-up list. They
// are synthetic: the credential-shaped one is assembled at run time from
// harmless pieces and never written whole, which is this tree's rule, and none
// is recorded from any installation (`belong` least of all).
//
// **The never-send check is proved able to fail.** A predicate that returned
// nothing would pass this file while proving nothing, so each planted capture
// below is run through it and must be caught: the message's text behind an
// allowed event, a credential-shaped string in a parameter, and a parameter the
// allow-list does not carry. A test whose failure mode is "the app grew a
// channel" has to be shown a leak before it is trusted.
//
// **What it does not claim.** What the *native* SDK collects on its own, and
// what a real crash report carries -- the device model, the OS version, the
// stack -- is the build's and the console's, and only the plan's checks 120 and
// 121 can show it.

import assert from 'node:assert/strict';
import test from 'node:test';

import { secretShaped } from '../accounts/settings.js';
import { BREADCRUMBS, breadcrumb } from './breadcrumbs.js';
import {
  ACCOUNT_KINDS,
  EVENTS,
  EVENT_NAMES,
  EVENT_PARAMETERS,
  PAIRING_OUTCOMES,
  REFUSAL_REASONS,
  SCREENS,
  TELEMETRY_CODES,
  createTelemetry,
  telemetryEvent,
} from './events.js';

/** D114's tuple, with D111's item for the Auth credential. */
const ACCOUNT = Object.freeze({
  id: 'laptop',
  firebaseProjectId: 'fugaro-dev',
  authUid: 'AbCdEf0123456789ghIJkl',
  fugaroProject: 'fugaro',
});

const T0 = 1_700_000_000_000;

/** A mailbox message as D113 signs it: every field §7.1 names, and no more. */
const MESSAGE = Object.freeze({
  id: '-NfixtureMessageKey',
  kind: 'prompt',
  text: 'the fixture utterance that must never leave the phone',
  sender: 'laptop',
  at: T0,
  mac: 'synthetic-mac-key-0123456789abcdef',
});

/** D111's Auth credential, assembled rather than written whole. */
const TOKEN = ['eyJhbGciOiJub25lIn0', 'eyJzdWIiOiJ0ZXN0In0', 'c2lnbmF0dXJl'].join('.');

/** Every value the fixtures hold, and the reason the capture may hold none. */
const FIXTURE_VALUES = Object.freeze([
  ACCOUNT.id,
  ACCOUNT.firebaseProjectId,
  ACCOUNT.authUid,
  ACCOUNT.fugaroProject,
  MESSAGE.id,
  MESSAGE.text,
  MESSAGE.sender,
  MESSAGE.mac,
  TOKEN,
]);

/** The values a parameter is allowed to be: booleans, and the closed sets. */
const ALLOWED_VALUES = new Set([...SCREENS, ...ACCOUNT_KINDS, ...REFUSAL_REASONS, ...PAIRING_OUTCOMES]);

/** The events the app records, with the values it holds at each moment. */
const RECORDED = Object.freeze([
  [EVENTS.APP_LAUNCH, { first: true }],
  [EVENTS.ACCOUNT_ADDED, { kind: 'pairing' }],
  [EVENTS.ACCOUNT_SWITCHED, {}],
  [EVENTS.SCREEN_SHOWN, { screen: 'mailbox' }],
  [EVENTS.COMMAND_REFUSED, { reason: 'offline' }],
  [EVENTS.PAIRING_FINISHED, { outcome: 'ok' }],
  [EVENTS.CRASH_REPORTED, {}],
]);

/** The same calls with the fixture's own values where a closed value belongs. */
const PLANTED = Object.freeze([
  [EVENTS.SCREEN_SHOWN, { screen: MESSAGE.text }],
  [EVENTS.ACCOUNT_ADDED, { kind: ACCOUNT.fugaroProject }],
  [EVENTS.PAIRING_FINISHED, { outcome: TOKEN }],
  [EVENTS.APP_LAUNCH, { first: ACCOUNT.authUid }],
  [EVENTS.ACCOUNT_SWITCHED, { message: MESSAGE }],
  ['message_sent', { text: MESSAGE.text }],
  [EVENTS.SCREEN_SHOWN, { screen: 'mailbox', text: MESSAGE.text }],
]);

/** The fake port: the SDK's own surface, and nothing else. */
function capturing() {
  const capture = [];
  return {
    capture,
    port: {
      logEvent(name, parameters) {
        capture.push(Object.freeze([name, parameters]));
      },
    },
  };
}

/** Every string a capture holds, walked. */
function stringsIn(value, found = []) {
  if (typeof value === 'string') {
    found.push(value);
  } else if (value !== null && typeof value === 'object') {
    for (const nested of Object.values(value)) stringsIn(nested, found);
  }
  return found;
}

/**
 * What a capture holds that may not leave the phone: a fixture's own value, a
 * credential-shaped string, an event outside the allow-list, a parameter the
 * event does not carry, or a value that is neither a boolean nor an enum
 * member. Returns the reasons, so "the capture is clean" and "this planted
 * capture is not" are the same question asked twice.
 */
function offences(capture) {
  const found = [];
  const text = JSON.stringify(capture);
  for (const value of FIXTURE_VALUES) {
    if (text.includes(value)) found.push(`fixture:${value}`);
  }
  for (const string of stringsIn(capture)) {
    const shaped = secretShaped(string);
    if (shaped !== null) found.push(`shaped:${shaped}`);
  }
  for (const [name, parameters] of capture) {
    if (!EVENT_NAMES.includes(name)) found.push(`event:${name}`);
    for (const [key, value] of Object.entries(parameters)) {
      if (!Object.hasOwn(EVENT_PARAMETERS[name] ?? {}, key)) found.push(`parameter:${key}`);
      if (typeof value !== 'boolean' && !ALLOWED_VALUES.has(value)) found.push(`value:${String(value)}`);
    }
  }
  return found;
}

test('the never-send check can fail: each planted capture is caught', () => {
  // The control the whole file rests on. If `offences` returned nothing for
  // these, every assertion below would be a test about a predicate that had
  // stopped looking.
  for (const planted of [
    [[EVENTS.SCREEN_SHOWN, { screen: MESSAGE.text }]],
    [[EVENTS.APP_LAUNCH, { first: true, token: TOKEN }]],
    [[EVENTS.ACCOUNT_ADDED, { kind: 'manual', project: ACCOUNT.firebaseProjectId }]],
    [['message_sent', { text: MESSAGE.text }]],
    [[EVENTS.CRASH_REPORTED, { screen: 'not-a-screen' }]],
  ]) {
    assert.notDeepEqual(offences(planted), [], JSON.stringify(planted));
  }
  // ... and the credential-shaped fixture really is one, so the walk above is
  // looking for something.
  assert.equal(secretShaped(TOKEN), 'jwt');
  assert.equal(secretShaped(MESSAGE.text), null);
});

test('every event the app records carries a boolean or a member of its closed set', () => {
  const { capture, port } = capturing();
  const telemetry = createTelemetry(port);
  for (const [name, parameters] of RECORDED) {
    const made = telemetry.record(name, parameters);
    assert.equal(made.ok, true, name);
  }
  assert.deepEqual(capture.map(([name]) => name), RECORDED.map(([name]) => name));
  assert.deepEqual(offences(capture), []);
});

test('the fixture’s own values are refused at the door, and a refusal sends nothing', () => {
  const { capture, port } = capturing();
  const telemetry = createTelemetry(port);
  for (const [name, parameters] of PLANTED) {
    const before = capture.length;
    const made = telemetry.record(name, parameters);
    assert.equal(made.ok, false, `${name} ${JSON.stringify(parameters)}`);
    assert.equal(capture.length, before, `${name}: a refused event is not sent`);
    // The refusal names the field and never the value: a refusal that echoed
    // what it refused would be the leak it exists to stop.
    assert.equal(JSON.stringify(made).includes(MESSAGE.text), false, name);
    assert.equal(JSON.stringify(made).includes(TOKEN), false, name);
  }
  assert.deepEqual(capture, []);
});

test('an event is sent only with the parameters its own entry declares', () => {
  const made = telemetryEvent(EVENTS.SCREEN_SHOWN, { screen: 'overview' });
  assert.equal(made.ok, true);
  assert.deepEqual(made.event, { name: 'screen_shown', parameters: { screen: 'overview' } });

  assert.equal(telemetryEvent('consent_set', { granted: true }).code, TELEMETRY_CODES.UNKNOWN_EVENT);
  assert.equal(telemetryEvent(EVENTS.ACCOUNT_ADDED, {}).code, TELEMETRY_CODES.MISSING_PARAMETER);
  assert.equal(telemetryEvent(EVENTS.APP_LAUNCH, { first: true, name: 'laptop' }).code, TELEMETRY_CODES.UNKNOWN_PARAMETER);
  assert.equal(telemetryEvent(EVENTS.COMMAND_REFUSED, { reason: 'because' }).code, TELEMETRY_CODES.PARAMETER_VALUE);
  assert.equal(telemetryEvent(EVENTS.APP_LAUNCH, { first: 'yes' }).code, TELEMETRY_CODES.PARAMETER_VALUE);
  assert.equal(telemetryEvent(EVENTS.SCREEN_SHOWN, { screen: 'settings' }).code, TELEMETRY_CODES.PARAMETER_VALUE);
});

test('every name §7.2 allows has an entry, and no other name does', () => {
  assert.deepEqual([...EVENT_NAMES].sort(), [
    'account_added',
    'account_switched',
    'app_launch',
    'command_refused',
    'crash_reported',
    'pairing_finished',
    'screen_shown',
  ]);
  assert.deepEqual(Object.keys(EVENT_PARAMETERS).sort(), [...EVENT_NAMES].sort());
  // D255's always-on answer removed the sheet, and the event that recorded its
  // answer went with it: the allow-list is one name shorter, not one name
  // carrying a parameter nothing can produce.
  assert.equal(EVENT_NAMES.includes('consent_set'), false);
  // §7.2: `settings` is deliberately not a screen -- the app has none, so a
  // value for it would name a screen that cannot be reached.
  assert.equal(SCREENS.includes('settings'), false);
});

test('a breadcrumb is a constant from the module’s list, or it is redacted', () => {
  assert.ok(BREADCRUMBS.includes('screen_mailbox'));
  assert.equal(breadcrumb('screen_mailbox'), 'screen_mailbox');

  const uttered = `the mailbox said ${TOKEN} out loud`;
  const redacted = breadcrumb(uttered, [TOKEN]);
  assert.equal(redacted, 'the mailbox said [REDACTED] out loud');
  assert.equal(redacted.includes(TOKEN), false);

  // P1.3's own refusals are not swallowed: a breadcrumb that is neither a
  // constant nor text is a refusal, not a value that quietly passes through.
  assert.throws(() => breadcrumb(MESSAGE, [TOKEN]));
  assert.throws(() => breadcrumb(uttered, 'not-a-list'));
  // An empty list refuses too, and that control is the one that matters: the
  // redactor's own forms of nothing are nothing, so the text would have gone
  // out whole -- the one failure §7.3's rule exists to make impossible.
  assert.throws(() => breadcrumb(uttered, []));
  assert.equal(breadcrumb('screen_mailbox', []), 'screen_mailbox');
});
