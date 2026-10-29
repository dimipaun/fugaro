// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
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
// **The Android half (added 2026-10-21, the launch crash).** The accessibility
// levels are **iOS's**, and only iOS's: `expo-secure-store`'s Swift module
// declares seven `Constant(...)` entries, and its Kotlin module declares
// **none** — so on Android the SDK's own
// `export const WHEN_UNLOCKED_THIS_DEVICE_ONLY = ExpoSecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY`
// evaluates to `undefined`. The port used to require that constant whatever the
// platform, and `mobile/index.js` builds the port while the module is still
// being evaluated — before React renders anything — so an Android build died on
// open with no screen at all, which is the crash the owner reported for the
// `preview` APK. The port takes the **platform** now, because that is the fact
// that decides this: on iOS the constant is still required and a rename is
// still a loud failure; on Android it is not asked for and the write carries no
// `keychainAccessible` (the Kotlin `SecureStoreOptions` record has no such
// field). The tests below pin both halves, including that an Android port is
// built from a module that has no accessibility constants **at all**.
//
// What is NOT tested here: the SDK. `expo-secure-store` is not installed in CI,
// so its real behaviour, its constant's real value and the platform's own
// guarantees are unverified by anything in this tree. That is listed in
// `mobile/README.md` and in the pull request. The one thing that IS read off
// the installed package rather than assumed is the asymmetry itself: the Kotlin
// module's `Constant(` count is zero and the Swift module's is seven, which is
// quoted in `mobile/README.md`.

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

/**
 * The Android module, as the package really declares it: a module with the
 * three methods and **no accessibility constant at all**. The iOS fake above
 * cannot stand in for it, and that is the point of the test that uses this.
 */
function androidSDK() {
  const sdk = fakeSDK();
  delete sdk.WHEN_UNLOCKED_THIS_DEVICE_ONLY;
  delete sdk.WHEN_UNLOCKED;
  return sdk;
}

test('a write carries THIS_DEVICE_ONLY, taken from the module', async () => {
  const sdk = fakeSDK({ accessible: 'THIS_DEVICE_ONLY' });
  const port = expoSecureStorePort(sdk, { platform: 'ios' });
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
  const port = expoSecureStorePort(sdk, { platform: 'ios' });
  await port.setItem('k', 'v');
  assert.equal(sdk.calls[0][3].keychainAccessible, 'SOME_OTHER_PLATFORM_CONSTANT');
  assert.notEqual(port.keychainAccessible, 'THIS_DEVICE_ONLY');
});

test('reads and deletes address the item by key, and set no accessibility', async () => {
  const sdk = fakeSDK();
  const port = expoSecureStorePort(sdk, { platform: 'ios' });
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
  assert.throws(() => expoSecureStorePort(withoutConstant, { platform: 'ios' }), /WHEN_UNLOCKED_THIS_DEVICE_ONLY/);
  assert.throws(() => expoSecureStorePort(null, { platform: 'ios' }), TypeError);
  assert.throws(() => expoSecureStorePort('expo-secure-store', { platform: 'ios' }), TypeError);
  for (const method of ['getItemAsync', 'setItemAsync', 'deleteItemAsync']) {
    const broken = fakeSDK();
    broken[method] = 'not a function';
    assert.throws(() => expoSecureStorePort(broken, { platform: 'ios' }), new RegExp(method));
  }
});

test('the port has no keys(): the device cannot enumerate the Keychain', () => {
  const port = expoSecureStorePort(fakeSDK(), { platform: 'ios' });
  // A `keys()` here would be a promise the platform cannot keep, and
  // `secure-store.js` is written so that nothing asks for one. `MemorySecureStore`
  // has one for the tests alone; the real port must not pretend.
  assert.equal('keys' in port, false);
  assert.deepEqual(
    Object.keys(port).sort(),
    ['deleteItem', 'getItem', 'keychainAccessible', 'platform', 'setItem'],
  );
});

// --- the Android half: the accessibility levels are iOS's alone -------------
//
// Mutation for every test below: drop the platform argument from the port (or
// keep requiring `WHEN_UNLOCKED_THIS_DEVICE_ONLY` unconditionally) and the first
// one goes red with the message the Android APK died on -- before any screen.

test('an Android module with no accessibility constant builds a port, and writes with no options', async () => {
  // This is the launch crash, as a test. The Kotlin module declares no
  // `Constant(...)`, so `WHEN_UNLOCKED_THIS_DEVICE_ONLY` is `undefined` on
  // Android; the port is built by `mobile/index.js` while the module is still
  // evaluating, so a port that insisted on the constant threw before React
  // rendered -- an app that opens and closes with no screen at all.
  const sdk = androidSDK();
  assert.equal(sdk.WHEN_UNLOCKED_THIS_DEVICE_ONLY, undefined, 'the premise: Android has no such constant');

  const port = expoSecureStorePort(sdk, { platform: 'android' });
  assert.equal(port.platform, 'android');
  assert.equal(port.keychainAccessible, null, 'there is no accessibility attribute to name on Android');

  await port.setItem('fugaro.account.laptop.idToken', 'synthetic');
  assert.deepEqual(sdk.calls, [
    ['setItemAsync', 'fugaro.account.laptop.idToken', 'synthetic', undefined],
  ], 'the write carries no keychainAccessible: the Kotlin options record has no such field');
  // ... and the item is still readable through the port, which is the whole
  // point: Android's storage works, it just has no accessibility levels.
  assert.equal(await port.getItem('fugaro.account.laptop.idToken'), 'synthetic');
  await port.deleteItem('fugaro.account.laptop.idToken');
  assert.equal(await port.getItem('fugaro.account.laptop.idToken'), null);
});

test('the iOS rule is unchanged: the constant is required where the platform has it', () => {
  // The other half of the fence, and the reason the platform is an argument
  // rather than "write the option when the module happens to define it": on
  // iOS a renamed constant must still refuse the port, because writing an item
  // with the platform default is an item that rides an iCloud backup onto
  // another phone.
  const { WHEN_UNLOCKED_THIS_DEVICE_ONLY: _omitted, ...withoutConstant } = fakeSDK();
  assert.throws(
    () => expoSecureStorePort(withoutConstant, { platform: 'ios' }),
    /WHEN_UNLOCKED_THIS_DEVICE_ONLY/,
  );
  // ... while the same module IS a usable Android module, so the two rules are
  // decided by the platform and not by what the module happens to carry.
  assert.equal(
    expoSecureStorePort(withoutConstant, { platform: 'android' }).keychainAccessible,
    null,
  );
});

test('a port built without a known platform is refused, naming the platforms it knows', () => {
  // Fail closed on the one input that decides the rule above. This throw is
  // safe to keep loud: `mobile/index.js` builds the port inside the guarded
  // startup, so a wiring mistake here renders the failure screen instead of
  // closing the app (`src/app/startup.js`).
  for (const bad of [undefined, null, {}, { platform: 'web' }, { platform: '' }, { platform: 'ios ' }]) {
    assert.throws(
      () => expoSecureStorePort(fakeSDK(), bad),
      /platform/,
      `the port must refuse ${JSON.stringify(bad)}`,
    );
  }
  // The two it knows are accepted, and they are the two the app can be built
  // for (`app.json` declares `ios` and `android` and nothing else).
  assert.equal(expoSecureStorePort(fakeSDK(), { platform: 'android' }).platform, 'android');
  assert.equal(expoSecureStorePort(fakeSDK(), { platform: 'ios' }).platform, 'ios');
});
