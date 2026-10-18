// The Keychain port's tests (mobile plan, Task P3.1; decision D111).
//
// There is no Keychain in CI and no test here may need a device (Global
// Constraint 10), so the SDK is faked -- but the thing this file pins is not a
// mock's behaviour, it is **the one security decision the adapter makes**: the
// item is written with `WHEN_UNLOCKED_THIS_DEVICE_ONLY`, read from the module
// rather than guessed. An item written with the platform's default
// accessibility is restored onto another phone and rides along in an iCloud
// backup, which is D111's "per-pairing" key ceasing to be per-pairing.
//
// The assertions are therefore about the CALLS the adapter makes: which
// accessibility constant it passes, that the value came from the module (a
// second fake with a different constant must be written with that one, so a
// hardcoded literal is red), and that a module which cannot promise the
// accessibility is refused at construction rather than silently downgraded.
//
// What is NOT tested here: the SDK. `expo-secure-store` is not installed in
// this environment, so its real behaviour, its constant's real value and the
// platform's own guarantees are unverified by anything in this tree. That is
// listed in `mobile/README.md` and in the pull request.

import assert from 'node:assert/strict';
import test from 'node:test';

import { expoSecureStorePort } from './expo-secure-store.js';

/** A stand-in for the SDK: records every call, answers from a map. */
function fakeSDK({ accessible = 'THIS_DEVICE_ONLY', store = new Map() } = {}) {
  const calls = [];
  return {
    calls,
    store,
    WHEN_UNLOCKED_THIS_DEVICE_ONLY: accessible,
    WHEN_UNLOCKED: 'WHEN_UNLOCKED',
    async getItemAsync(key, options) {
      calls.push(['getItemAsync', key, options]);
      return store.has(key) ? store.get(key) : null;
    },
    async setItemAsync(key, value, options) {
      calls.push(['setItemAsync', key, value, options]);
      store.set(key, value);
    },
    async deleteItemAsync(key, options) {
      calls.push(['deleteItemAsync', key, options]);
      store.delete(key);
    },
  };
}

test('a write carries THIS_DEVICE_ONLY, taken from the module', async () => {
  const sdk = fakeSDK({ accessible: 'THIS_DEVICE_ONLY' });
  const port = expoSecureStorePort(sdk);
  await port.setItem('fugaro.account.laptop.idToken', 'synthetic');
  assert.deepEqual(sdk.calls, [
    ['setItemAsync', 'fugaro.account.laptop.idToken', 'synthetic', {
      keychainAccessible: 'THIS_DEVICE_ONLY',
    }],
  ]);
  assert.equal(port.keychainAccessible, 'THIS_DEVICE_ONLY');
});

test('the accessibility is the module\'s, so a hardcoded literal is red', async () => {
  // A second module with a different constant: the port must write THAT one.
  // If the adapter hardcoded a plausible value, this is where it shows.
  const sdk = fakeSDK({ accessible: 'SOME_OTHER_PLATFORM_CONSTANT' });
  const port = expoSecureStorePort(sdk);
  await port.setItem('k', 'v');
  assert.equal(sdk.calls[0][3].keychainAccessible, 'SOME_OTHER_PLATFORM_CONSTANT');
  assert.notEqual(port.keychainAccessible, 'THIS_DEVICE_ONLY');
});

test('reads and deletes address the item by key, and set no accessibility', async () => {
  const sdk = fakeSDK();
  const port = expoSecureStorePort(sdk);
  await port.setItem('k', 'v');
  assert.equal(await port.getItem('k'), 'v');
  assert.equal(await port.getItem('absent'), null, 'absence is a value, not an error');
  await port.deleteItem('k');
  assert.equal(await port.getItem('k'), null);
  // Accessibility is an attribute written with the item; a read or a delete
  // that tried to set it would be inventing a query the platform does not have.
  assert.deepEqual(sdk.calls.map(([method, key, third, fourth]) => [
    method,
    key,
    method === 'setItemAsync' ? fourth : third,
  ]), [
    ['setItemAsync', 'k', { keychainAccessible: 'THIS_DEVICE_ONLY' }],
    ['getItemAsync', 'k', undefined],
    ['getItemAsync', 'absent', undefined],
    ['deleteItemAsync', 'k', undefined],
    ['getItemAsync', 'k', undefined],
  ]);
});

test('a module that cannot promise THIS_DEVICE_ONLY is refused outright', () => {
  // The dangerous case: an SDK update renames the constant. Storing the
  // credential anyway, with the platform default, is a key that leaves the
  // device -- so the port refuses to exist instead.
  const { WHEN_UNLOCKED_THIS_DEVICE_ONLY: _omitted, ...withoutConstant } = fakeSDK();
  assert.throws(() => expoSecureStorePort(withoutConstant), /WHEN_UNLOCKED_THIS_DEVICE_ONLY/);
  assert.throws(() => expoSecureStorePort(null), TypeError);
  assert.throws(() => expoSecureStorePort('expo-secure-store'), TypeError);
  for (const method of ['getItemAsync', 'setItemAsync', 'deleteItemAsync']) {
    const broken = fakeSDK();
    broken[method] = 'not a function';
    assert.throws(() => expoSecureStorePort(broken), new RegExp(method));
  }
});

test('the port has no keys(): the device cannot enumerate the Keychain', () => {
  const port = expoSecureStorePort(fakeSDK());
  // A `keys()` here would be a promise the platform cannot keep, and
  // `secure-store.js` is written so that nothing asks for one. `MemorySecureStore`
  // has one for the tests alone; the real port must not pretend.
  assert.equal('keys' in port, false);
  assert.deepEqual(Object.keys(port).sort(), ['deleteItem', 'getItem', 'keychainAccessible', 'setItem']);
});
