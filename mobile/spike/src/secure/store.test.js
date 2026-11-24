// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The synced-keychain probe: the item, the option, and the reading (task M0;
// Check 733's U10).

import assert from 'node:assert/strict';
import test from 'node:test';

import { ACCESSIBLE, ITEM_NAMES, ITEM_PREFIX, KEYCHAIN_OPTIONS, RESTORE_MEANING, itemKey, readBack, writeOptions } from './store.js';

test('the probe writes in the spike\'s own namespace, never the app\'s', () => {
  // A probe item that landed on an app key would be a spike writing where a
  // paired device's material lives -- so the prefix is the spike's and the
  // names are a closed set.
  assert.equal(itemKey(ITEM_NAMES.sync), `${ITEM_PREFIX}${ITEM_NAMES.sync}`);
  assert.equal(itemKey('u10-keychain-probe'), 'fugaro.spike.u10-keychain-probe');
  assert.equal(itemKey('u10-keychain-probe').startsWith('fugaro.account.'), false);
  for (const name of ['', 'probe', '../fugaro.account.token', undefined]) {
    assert.throws(() => itemKey(name), /is not one of this spike's items/);
  }
});

test('the write asks iOS for WHEN_UNLOCKED and Android for no level at all', () => {
  // The Android half is not an omission: expo-secure-store's Android side takes
  // no accessibility level (the Kotlin options record has no such field), and
  // asking for one is the crash mobile/README.md records.
  assert.deepEqual(writeOptions('ios'), { keychainAccessible: 'WHEN_UNLOCKED' });
  assert.equal(ACCESSIBLE.ios, 'WHEN_UNLOCKED');
  assert.equal(ACCESSIBLE.ios.endsWith('THIS_DEVICE_ONLY'), false, 'the app\'s own level, and the reason the probe writes another');
  assert.deepEqual(writeOptions('android'), {});
  assert.equal(ACCESSIBLE.android, null);
});

test('the option list says no exported option is a sync switch', () => {
  assert.equal(KEYCHAIN_OPTIONS.synchronizable, false);
  assert.equal(KEYCHAIN_OPTIONS.levels.length, 5);
  assert.ok(KEYCHAIN_OPTIONS.levels.includes('WHEN_UNLOCKED'));
  assert.ok(KEYCHAIN_OPTIONS.levels.includes('WHEN_UNLOCKED_THIS_DEVICE_ONLY'));
  assert.match(KEYCHAIN_OPTIONS.why, /kSecAttrSynchronizable/);
  assert.match(RESTORE_MEANING, /M3, K3/);
});

test('a read-back says whether the item is there and how long it is, never what it holds', () => {
  assert.deepEqual(readBack({ value: 'probe-123', expected: 'probe-123' }), { present: true, length: 9, matches: true });
  assert.deepEqual(readBack({ value: 'other', expected: 'probe-123' }), { present: true, length: 5, matches: false });
  assert.deepEqual(readBack({ value: undefined, expected: 'probe-123' }), { present: false, length: 0, matches: false });
  assert.deepEqual(readBack({ value: null, expected: 'probe-123' }), { present: false, length: 0, matches: false });
  // The one shape there is: no field of a read-back can carry the stored value.
  const read = readBack({ value: 'a-secret-ish-value', expected: 'a-secret-ish-value' });
  assert.equal(JSON.stringify(read).includes('a-secret-ish-value'), false);
});
