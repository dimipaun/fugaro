// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The settings port's tests (mobile plan, Task P3.1; D111, D114).
//
// `expo-async-storage.js` is the unencrypted store -- the "settings file" D111
// says the two items must never be in -- so what this file pins is that the
// port is a faithful, boring translation of the SDK's four methods, and that
// the two stores differ in exactly the way the two modules claim: the settings
// store CAN enumerate (so `AccountStore.list()` is a real read) and the
// Keychain port CANNOT (so `secure-store.js` probes its declared item names).
//
// What is NOT tested: AsyncStorage itself, which is not installed here. The
// demo in `shared/`'s convention: a fake module, not a fake device.

import assert from 'node:assert/strict';
import test from 'node:test';

import { asyncStoragePort } from './expo-async-storage.js';
import { expoSecureStorePort } from './expo-secure-store.js';
import { SettingsStore } from './settings.js';

function fakeAsyncStorage({ store = new Map() } = {}) {
  const calls = [];
  return {
    calls,
    store,
    async getItem(key) {
      calls.push(['getItem', key]);
      return store.has(key) ? store.get(key) : null;
    },
    async setItem(key, value) {
      calls.push(['setItem', key, value]);
      store.set(key, value);
    },
    async removeItem(key) {
      calls.push(['removeItem', key]);
      store.delete(key);
    },
    async getAllKeys() {
      calls.push(['getAllKeys']);
      return [...store.keys()];
    },
  };
}

test('the port translates AsyncStorage\'s four methods, and renames one', async () => {
  const sdk = fakeAsyncStorage();
  const port = asyncStoragePort(sdk);
  await port.setItem('fugaro.settings.account.laptop', '{"id":"laptop"}');
  assert.equal(await port.getItem('fugaro.settings.account.laptop'), '{"id":"laptop"}');
  assert.equal(await port.getItem('never'), null, 'the SDK uses null, and so does the port');
  assert.deepEqual(await port.keys(), ['fugaro.settings.account.laptop']);
  // The port's name for the operation is `deleteItem`; the SDK's is
  // `removeItem`. One rename, tested, so the settings store needs no dialect.
  await port.deleteItem('fugaro.settings.account.laptop');
  assert.deepEqual(sdk.calls.at(-1), ['removeItem', 'fugaro.settings.account.laptop']);
  assert.deepEqual(await port.keys(), []);
});

test('a key list the SDK hands back oddly is still a list', async () => {
  const sdk = fakeAsyncStorage();
  const port = asyncStoragePort(sdk);
  assert.deepEqual(await port.keys(), []);
  sdk.getAllKeys = async () => null;
  assert.deepEqual(await port.keys(), [], 'a null key list is an empty one, never a crash');
});

test('a module missing any of the four methods is refused outright', () => {
  assert.throws(() => asyncStoragePort(null), TypeError);
  assert.throws(() => asyncStoragePort({}), /getItem/);
  for (const method of ['getItem', 'setItem', 'removeItem', 'getAllKeys']) {
    const broken = fakeAsyncStorage();
    broken[method] = undefined;
    assert.throws(() => asyncStoragePort(broken), new RegExp(method));
  }
});

test('the two stores differ in exactly the way the two modules claim', async () => {
  const settings = asyncStoragePort(fakeAsyncStorage());
  // iOS, because the port asks which platform it is: the accessibility constant
  // it reads here is the Keychain's, and the Keychain is iOS's
  // (`expo-secure-store.js`'s header has the Android half of that).
  const keychain = expoSecureStorePort({
    WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'THIS_DEVICE_ONLY',
    async getItemAsync() { return null; },
    async setItemAsync() {},
    async deleteItemAsync() {},
  }, { platform: 'ios' });
  // The settings store enumerates, so a list of accounts is a read ...
  assert.equal(typeof settings.keys, 'function');
  // ... and the Keychain does not, which is why D111's item names are a fixed
  // list of two and `items()` probes them instead of listing anything.
  assert.equal('keys' in keychain, false);
});

test('a settings store over the real port shape round-trips a tuple', async () => {
  // The seam, end to end and still with no device: a fake module, the real
  // port, the real settings store, and D114's tuple written and read back.
  const port = asyncStoragePort(fakeAsyncStorage());
  const store = new SettingsStore(port);
  const tuple = { id: 'laptop', firebaseProjectId: 'fugaro-dev', authUid: 'uid-0001', fugaroProject: 'fugaro' };
  assert.equal((await store.set('account.laptop', tuple)).ok, true);
  assert.deepEqual((await store.get('account.laptop')).value, tuple);
  assert.deepEqual([...(await store.keys()).keys], ['account.laptop']);
});
