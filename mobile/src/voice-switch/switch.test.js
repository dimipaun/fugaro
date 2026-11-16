// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The switch the app holds: its tests (mobile plan, Task P5.3.6; D114, D123,
// D127; the design is docs/mobile/design/voice-evaluation-switch.md §4.3).
//
// The plan's sentence this file is here for: **a remote call is not attempted
// without a recorded consent for that provider name** -- the guard the app can
// hold by itself, whose call is P5.3.7's. The strongest test of it is the one
// that hands `indicator` a context claiming an agreement the device does not
// have: the consent a badge is built from is the record's, read from the store,
// and a caller cannot assert one in.
//
// **No credential-shaped literal appears below.** The names are provider names,
// the dates epoch milliseconds, and the store is the app's own in-memory port.

import assert from 'node:assert/strict';
import test from 'node:test';

import { MemorySettingsStore, SETTINGS_KEY_PREFIX, SettingsStore } from '../accounts/index.js';
import { CALL_STATE, ENGINE, REASON, STATE } from '../../../shared/voice-switch/index.js';
import { createVoiceSwitch } from './switch.js';

const ACCOUNT = 'belong';
const OTHER = 'laptop';
const NAME = 'deepgram';
const AT = 1700000000000;

const admits = { ok: true, providers: [NAME], direction: { stt: true, tts: true }, reason: null };
const agreed = [{ provider: NAME, at: AT }];

function makeSwitch(id = ACCOUNT) {
  const port = new MemorySettingsStore();
  return { port, voice: createVoiceSwitch({ settings: new SettingsStore(port), accountId: id }) };
}

test('a fresh account is local on both directions, and has agreed to nothing', async () => {
  const { voice } = makeSwitch();
  const record = await voice.read();
  assert.equal(record.ok, true);
  assert.deepEqual({ ...record.settings }, { stt: 'local', tts: 'local' });
  assert.deepEqual([...record.consent], []);
  for (const direction of ['stt', 'tts']) {
    const badge = await voice.indicator({ direction, status: admits });
    assert.equal(badge.state, STATE.LOCAL, direction);
    assert.equal(badge.reason, REASON.SWITCH_LOCAL, direction);
    assert.equal(badge.send, false, direction);
  }
});

test('a direction can be turned on, and the other is left alone', async () => {
  const { voice } = makeSwitch();
  const written = await voice.set('stt', ENGINE.REMOTE);
  assert.equal(written.ok, true);
  assert.deepEqual({ ...written.settings }, { stt: 'remote', tts: 'local' });
  const record = await voice.read();
  assert.deepEqual({ ...record.settings }, { stt: 'remote', tts: 'local' }, 'the other direction is what it was');
  const off = await voice.indicator({ direction: 'tts', status: admits });
  assert.equal(off.state, STATE.LOCAL);
  assert.equal(off.reason, REASON.SWITCH_LOCAL, 'the switch is per direction');
  const on = await voice.indicator({ direction: 'stt', status: admits });
  assert.equal(on.state, STATE.LOCAL);
  assert.equal(on.reason, REASON.NO_CONSENT, 'on, and still not sending: no consent is recorded');
});

test('setting a direction the app does not have, or an engine that is not one, is refused and writes nothing', async () => {
  const { port, voice } = makeSwitch();
  for (const [direction, engine, code] of [
    ['both', ENGINE.REMOTE, 'unknown-direction'],
    ['', ENGINE.REMOTE, 'unknown-direction'],
    [null, ENGINE.REMOTE, 'unknown-direction'],
    ['stt', 'auto', 'unknown-engine'],
    ['stt', true, 'unknown-engine'],
    ['stt', undefined, 'unknown-engine'],
  ]) {
    const refused = await voice.set(direction, engine);
    assert.equal(refused.ok, false, `${direction}/${engine}`);
    assert.equal(refused.code, code, `${direction}/${engine}`);
    assert.deepEqual(port.keys(), [], `${direction}/${engine} reached no store`);
  }
});

test('the consent sheet records one name and takes it back', async () => {
  const { voice } = makeSwitch();
  const recorded = await voice.recordConsent(NAME, AT);
  assert.equal(recorded.ok, true);
  assert.deepEqual(recorded.consent.map((item) => ({ ...item })), agreed);
  await voice.set('stt', ENGINE.REMOTE);
  const on = await voice.indicator({ direction: 'stt', status: admits });
  assert.equal(on.state, STATE.REMOTE);
  assert.equal(on.text, `remote (${NAME})`);
  assert.equal(on.send, true);

  const revoked = await voice.revokeConsent(NAME);
  assert.equal(revoked.ok, true);
  assert.deepEqual([...revoked.consent], []);
  const off = await voice.indicator({ direction: 'stt', status: admits });
  assert.equal(off.state, STATE.LOCAL);
  assert.equal(off.reason, REASON.NO_CONSENT, 'the switch is still on; the agreement is what went');
  assert.equal(off.send, false);
});

test('a consent the app cannot judge is refused, and the record it holds is untouched', async () => {
  const { voice } = makeSwitch();
  await voice.recordConsent(NAME, AT);
  for (const [provider, at] of [['https://provider.example', AT], [NAME, 0], [NAME, undefined]]) {
    const refused = await voice.recordConsent(provider, at);
    assert.equal(refused.ok, false, `${provider}/${at}`);
  }
  const refused = await voice.revokeConsent('not a name at all');
  assert.equal(refused.ok, false);
  const record = await voice.read();
  assert.deepEqual(record.consent.map((item) => ({ ...item })), agreed, 'the record is what it was');
});

test('a caller\'s claimed consent is not the record: the device\'s own is the one judged', async () => {
  const { voice } = makeSwitch();
  await voice.set('stt', ENGINE.REMOTE);
  // The caller says the user agreed, and the device holds no such record. §4.3:
  // "the app refuses only what it can know by itself -- a provider for which it
  // holds no recorded consent".
  const badge = await voice.indicator({ direction: 'stt', status: admits, consent: agreed });
  assert.equal(badge.state, STATE.LOCAL);
  assert.equal(badge.reason, REASON.NO_CONSENT);
  assert.equal(badge.send, false, 'the guard that keeps P5.3.7 from sending is here');

  // And the same claim with the record behind it is the one that sends: the
  // test above is the guard, not a module that never sends.
  await voice.recordConsent(NAME, AT);
  const honest = await voice.indicator({ direction: 'stt', status: admits });
  assert.equal(honest.send, true);
});

test('a caller\'s claimed setting is not the record either', async () => {
  const { voice } = makeSwitch();
  const badge = await voice.indicator({ direction: 'stt', status: admits, settings: { stt: 'remote', tts: 'remote' } });
  assert.equal(badge.reason, REASON.SWITCH_LOCAL, 'the store is what answers, not the context');
  assert.equal(badge.send, false);
});

test('the two directions are asked separately, and a call in flight is this direction\'s', async () => {
  const { voice } = makeSwitch();
  await voice.set('stt', ENGINE.REMOTE);
  await voice.set('tts', ENGINE.REMOTE);
  await voice.recordConsent(NAME, AT);
  const inFlight = { direction: 'stt', engine: ENGINE.REMOTE, provider: NAME, state: CALL_STATE.IN_FLIGHT, reason: null };
  const stt = await voice.indicator({ direction: 'stt', status: admits, lastCallInDirection: inFlight });
  assert.equal(stt.inFlight, true);
  assert.equal(stt.send, false);
  const tts = await voice.indicator({ direction: 'tts', status: admits });
  assert.equal(tts.send, true, 'a call in the other direction is not this one\'s');
});

test('two accounts over one store do not share a switch or an agreement', async () => {
  const port = new MemorySettingsStore();
  const settings = new SettingsStore(port);
  const mine = createVoiceSwitch({ settings, accountId: ACCOUNT });
  const theirs = createVoiceSwitch({ settings, accountId: OTHER });
  await mine.set('stt', ENGINE.REMOTE);
  await mine.recordConsent(NAME, AT);
  const record = await theirs.read();
  assert.deepEqual({ ...record.settings }, { stt: 'local', tts: 'local' }, 'D114: separate, non-overlapping entries');
  assert.deepEqual([...record.consent], []);
  const badge = await theirs.indicator({ direction: 'stt', status: admits });
  assert.equal(badge.reason, REASON.SWITCH_LOCAL);
  assert.deepEqual(port.keys(), [`${SETTINGS_KEY_PREFIX}voice-${ACCOUNT}`], 'one account\'s writes are one key');
});

test('a store that cannot be read gives the fail-closed badge, not an exception', async () => {
  const failing = {
    get: async () => {
      throw new Error('the port is gone');
    },
    set: async () => {
      throw new Error('the port is gone');
    },
  };
  const voice = createVoiceSwitch({ settings: failing, accountId: ACCOUNT });
  const badge = await voice.indicator({ direction: 'stt', status: admits });
  assert.equal(badge.state, STATE.CHECKING);
  assert.equal(badge.engine, ENGINE.LOCAL);
  assert.equal(badge.send, false);
  assert.equal(badge.persistent, true);
  assert.equal(badge.refusal.code, 'store-failed', 'the badge carries the refusal so a screen can report it');
  assert.equal(badge.direction, 'stt');
  const written = await voice.set('stt', ENGINE.REMOTE);
  assert.equal(written.ok, false, 'and a write does not pretend to have landed');
});

test('an indicator asked with nothing at all is still a badge', async () => {
  const { voice } = makeSwitch();
  for (const context of [undefined, null, 'stt', 7, []]) {
    const badge = await voice.indicator(context);
    assert.equal(badge.direction, null, String(context));
    assert.equal(badge.engine, ENGINE.LOCAL, String(context));
    assert.equal(badge.send, false, String(context));
    assert.equal(badge.persistent, true, String(context));
  }
});

test('a switch without a store, or without an account, is refused at construction', () => {
  const settings = new SettingsStore(new MemorySettingsStore());
  assert.throws(() => createVoiceSwitch({ settings: null, accountId: ACCOUNT }), TypeError);
  assert.throws(() => createVoiceSwitch({ settings: {}, accountId: ACCOUNT }), TypeError, 'a store with no get() is not one');
  assert.throws(() => createVoiceSwitch({ settings: { get: async () => ({ ok: false, code: 'absent' }) }, accountId: ACCOUNT }), TypeError, 'nor one with no set()');
  assert.throws(() => createVoiceSwitch({ settings, accountId: 'not an id' }), TypeError, 'the key cannot be composed from it');
  assert.throws(() => createVoiceSwitch({ settings, accountId: undefined }), TypeError);
});
