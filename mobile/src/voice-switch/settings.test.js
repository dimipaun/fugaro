// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The record's rules: their tests (mobile plan, Task P5.3.6; D123, D127).
//
// The plan's verification sentence is the first test in this file -- **an unset
// setting is local on both directions** -- and the rest defend the two halves
// that sentence does not cover: that the key is per account and survives the
// settings store's own fence, and that a record which cannot be read comes back
// as a refusal rather than as a plausible default.
//
// **No credential-shaped literal appears below.** Everything written here is a
// project name, one of two words, an epoch millisecond count or a provider
// name, and the one test that asks what a credential looks like asks the app's
// own `secretShaped` rather than writing a value that would match it.

import assert from 'node:assert/strict';
import test from 'node:test';

import { MemorySettingsStore, SECRET_ITEMS, SETTINGS_CODES, SETTINGS_KEY_PREFIX, SettingsStore, secretShaped } from '../accounts/index.js';
import { REFUSAL } from '../../../shared/voice-switch/index.js';
import {
  DEFAULT_VOICE_SETTINGS,
  VOICE_CODES,
  VOICE_RECORD_FIELDS,
  VOICE_SETTINGS_PREFIX,
  readVoiceRecord,
  voiceSettingsKey,
  writeVoiceRecord,
} from './settings.js';

const ACCOUNT = 'belong';
const OTHER = 'laptop';
const AT = 1700000000000;

/** The account record's one key under the settings namespace. */
const rawKey = (id) => `${SETTINGS_KEY_PREFIX}${VOICE_SETTINGS_PREFIX}${id}`;

function makeStore() {
  const port = new MemorySettingsStore();
  return { port, store: new SettingsStore(port) };
}

/** Every string anywhere in a parsed JSON value. */
function stringsIn(value, found = []) {
  if (typeof value === 'string') found.push(value);
  else if (Array.isArray(value)) for (const item of value) stringsIn(item, found);
  else if (value !== null && typeof value === 'object') for (const item of Object.values(value)) stringsIn(item, found);
  return found;
}

test('an unset setting is local on both directions, and no consent is recorded', async () => {
  const { store } = makeStore();
  const record = await readVoiceRecord(store, ACCOUNT);
  assert.equal(record.ok, true);
  assert.deepEqual({ ...record.settings }, { stt: 'local', tts: 'local' });
  assert.deepEqual([...record.consent], []);
  assert.deepEqual({ ...DEFAULT_VOICE_SETTINGS }, { stt: 'local', tts: 'local' }, 'the default is the one value in this module');
  assert.ok(Object.isFrozen(record.settings), 'a caller cannot edit the record it was handed');
});

test('the key is the prefix and the account id, and an id that is not one cannot compose a key', () => {
  assert.equal(voiceSettingsKey(ACCOUNT), 'voice-belong');
  assert.equal(voiceSettingsKey('a-1_B'), 'voice-a-1_B', 'the alphabet `expo-secure-store` accepts is the alphabet here');
  for (const id of ['', 'a.b', 'a b', 'a/b', null, undefined, 7, 'x'.repeat(65)]) {
    assert.throws(() => voiceSettingsKey(id), TypeError, `${String(id)} is not an account id`);
  }
});

test('two accounts do not share a record', async () => {
  const { store } = makeStore();
  const written = await writeVoiceRecord(store, ACCOUNT, { settings: { stt: 'remote', tts: 'remote' }, consent: [] });
  assert.equal(written.ok, true);
  const other = await readVoiceRecord(store, OTHER);
  assert.deepEqual({ ...other.settings }, { stt: 'local', tts: 'local' }, 'the other account has written nothing');
  assert.deepEqual([...other.consent], []);
});

test('the key survives the store fence for an account the fence would otherwise refuse', async () => {
  // `settings.js`'s word list refuses a KEY whose `.`-separated segment names a
  // credential, and the ids below are words an owner may give an installation.
  // This is the fault that list once caused (the `account.<id>` branch exists
  // because of it), and the single-segment key is what avoids repeating it.
  for (const id of ['token', 'macKey', 'secret', 'apiKey', 'idToken']) {
    const { store } = makeStore();
    const written = await writeVoiceRecord(store, id, { settings: { stt: 'remote', tts: 'local' }, consent: [] });
    assert.equal(written.ok, true, `${id} may turn its switch on`);
    const record = await readVoiceRecord(store, id);
    assert.equal(record.ok, true, id);
    assert.equal(record.settings.stt, 'remote', id);
  }
});

test('a record round-trips: what was written is what is read', async () => {
  const { store } = makeStore();
  const written = await writeVoiceRecord(store, ACCOUNT, {
    settings: { stt: 'remote', tts: 'local' },
    consent: [{ provider: 'deepgram', at: AT }],
  });
  assert.equal(written.ok, true);
  const record = await readVoiceRecord(store, ACCOUNT);
  assert.deepEqual({ ...record.settings }, { stt: 'remote', tts: 'local' });
  assert.deepEqual(record.consent.map((entry) => ({ ...entry })), [{ provider: 'deepgram', at: AT }]);
  assert.equal(written.consent[0].at, AT, 'the date is the caller\'s, not the module\'s');
});

test('the write is one key per account, and nothing else', async () => {
  const { port, store } = makeStore();
  await writeVoiceRecord(store, ACCOUNT, { settings: { stt: 'remote', tts: 'local' }, consent: [] });
  await writeVoiceRecord(store, OTHER, { settings: { stt: 'local', tts: 'remote' }, consent: [] });
  assert.deepEqual(port.keys(), [rawKey(ACCOUNT), rawKey(OTHER)].sort());
});

test('the write is under the settings namespace and never in the database', async () => {
  const { port, store } = makeStore();
  await writeVoiceRecord(store, ACCOUNT, { settings: { stt: 'remote', tts: 'local' }, consent: [] });
  for (const key of port.keys()) {
    assert.ok(key.startsWith(SETTINGS_KEY_PREFIX), key);
    assert.ok(!key.startsWith('fugaro.account.'), `${key} is not the secure store's namespace`);
  }
});

test('the record holds nothing secret-shaped, and the app still holds two items', async () => {
  const { port, store } = makeStore();
  await writeVoiceRecord(store, ACCOUNT, {
    settings: { stt: 'remote', tts: 'remote' },
    consent: [{ provider: 'deepgram', at: AT }],
  });
  const raw = await port.getItem(rawKey(ACCOUNT));
  for (const value of stringsIn(JSON.parse(raw))) {
    assert.equal(secretShaped(value), null, `${value} is not a credential`);
  }
  // Global Constraint 4, and the plan's own sentence: this module holds no
  // third item, and the app's list is still the two.
  assert.deepEqual([...SECRET_ITEMS], ['idToken', 'macKey']);
});

test('a stored record with a field this module does not have is refused', async () => {
  const { port, store } = makeStore();
  await port.setItem(
    rawKey(ACCOUNT),
    JSON.stringify({ settings: { stt: 'local', tts: 'local' }, consent: [], ...{ provider: 'deepgram' } }),
  );
  const record = await readVoiceRecord(store, ACCOUNT);
  assert.equal(record.ok, false);
  assert.equal(record.code, VOICE_CODES.UNKNOWN_FIELD);
  assert.deepEqual([...VOICE_RECORD_FIELDS], ['settings', 'consent'], 'the field set is the version');
});

test('a stored record whose values the shared rule cannot read is refused by that rule', async () => {
  const { port, store } = makeStore();
  const cases = [
    { value: { settings: { stt: 'auto', tts: 'local' }, consent: [] }, code: REFUSAL.UNKNOWN_ENGINE },
    { value: { settings: { stt: 'local', tts: 'local', extra: true }, consent: [] }, code: REFUSAL.UNKNOWN_FIELD },
    { value: { settings: 'remote', consent: [] }, code: VOICE_CODES.UNREADABLE_RECORD },
    { value: { settings: { stt: 'local', tts: 'local' }, consent: [{ provider: 'deepgram' }] }, code: REFUSAL.MISSING_FIELD },
    { value: { settings: { stt: 'local', tts: 'local' }, consent: [{ provider: 'deepgram', at: 0 }] }, code: REFUSAL.INVALID_AT },
    { value: { settings: { stt: 'local', tts: 'local' }, consent: [{ provider: 'https://provider.example', at: AT }] }, code: REFUSAL.BAD_PROVIDER },
    { value: { settings: { stt: 'local', tts: 'local' }, consent: 'deepgram' }, code: REFUSAL.INVALID_CONSENT },
  ];
  for (const { value, code } of cases) {
    await port.setItem(rawKey(ACCOUNT), JSON.stringify(value));
    const record = await readVoiceRecord(store, ACCOUNT);
    assert.equal(record.ok, false, JSON.stringify(value));
    assert.equal(record.code, code, JSON.stringify(value));
  }
});

test('a stored value that is not a record at all is refused, not read as defaults', async () => {
  const { port, store } = makeStore();
  for (const raw of ['[]', '7', '"local"', 'null']) {
    await port.setItem(rawKey(ACCOUNT), raw);
    const record = await readVoiceRecord(store, ACCOUNT);
    assert.equal(record.ok, false, raw);
    assert.equal(record.code, VOICE_CODES.UNREADABLE_RECORD, raw);
  }
});

test('a write refuses a record the shared rule cannot read, and writes nothing', async () => {
  const { port, store } = makeStore();
  for (const value of [
    { settings: { stt: 'auto', tts: 'local' }, consent: [] },
    { settings: { stt: 'remote', tts: 'remote' }, consent: [{ provider: 'deepgram' }] },
    { settings: { stt: 'remote', tts: 'remote' }, consent: [], other: 1 },
    'not a record',
  ]) {
    const written = await writeVoiceRecord(store, ACCOUNT, value);
    assert.equal(written.ok, false, JSON.stringify(value));
    assert.deepEqual(port.keys(), [], 'a refused record reaches no store');
  }
});

test('a store that cannot be read is a refusal, never an account with no settings', async () => {
  const failing = {
    get: async () => {
      throw new Error('the port is gone');
    },
    set: async () => {
      throw new Error('the port is gone');
    },
  };
  const record = await readVoiceRecord(failing, ACCOUNT);
  assert.equal(record.ok, false);
  assert.equal(record.code, SETTINGS_CODES.STORE_FAILED);
  const written = await writeVoiceRecord(failing, ACCOUNT, { settings: { stt: 'remote', tts: 'remote' }, consent: [] });
  assert.equal(written.ok, false);
  assert.equal(written.code, SETTINGS_CODES.STORE_FAILED);
});

test('a store that refuses for a reason of its own is not read as defaults', async () => {
  // `absent` is the one answer that means "this account has chosen nothing";
  // every other refusal is a fact about the store, and reporting it as an
  // account with no settings would hide it behind a plausible default.
  const refusing = {
    get: async () => Object.freeze({ ok: false, code: SETTINGS_CODES.UNREADABLE_VALUE }),
    set: async () => Object.freeze({ ok: false, code: SETTINGS_CODES.SECRET_VALUE }),
  };
  const record = await readVoiceRecord(refusing, ACCOUNT);
  assert.equal(record.ok, false);
  assert.equal(record.code, SETTINGS_CODES.UNREADABLE_VALUE);
  const written = await writeVoiceRecord(refusing, ACCOUNT, { settings: { stt: 'remote', tts: 'remote' }, consent: [] });
  assert.equal(written.ok, false);
  assert.equal(written.code, SETTINGS_CODES.SECRET_VALUE);
});

test('a store whose answer is not one of the store\'s own shapes is a refusal too', async () => {
  const strange = { get: async () => undefined, set: async () => 'ok' };
  const record = await readVoiceRecord(strange, ACCOUNT);
  assert.equal(record.ok, false);
  assert.equal(record.code, SETTINGS_CODES.STORE_FAILED);
});
