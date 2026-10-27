// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The settings port: `@react-native-async-storage/async-storage`, behind the
// four methods `settings.js` calls (mobile plan, Task P3.1; D111, D114).
//
// **This store is NOT encrypted, and that is the point of the file.** D111 says
// the phone's two items go in platform secure storage "and nowhere else", and
// "never in a settings file" -- so something has to be the settings file, and
// this is it. AsyncStorage is an unencrypted key-value store: on Android a
// file under the app's data directory, on iOS a plist in the app container.
// Anything written here is readable by anyone who can read the app's container
// (a backup, a rooted device, a debugger on a development build).
//
// So the tuple goes here -- a Firebase project id, an operator uid and a
// Fugaro project name are not secrets, and the app cannot list its accounts
// without them -- and the two items never do. `settings.js` is what holds that
// line, in three fences (a `Secret` cannot be a value, a credential-shaped
// value is refused, and a key that names an item or the secure store's
// namespace is refused), and `src/boundary.test.js` is what stops a second
// writer appearing here later. This file is deliberately the boring one: if a
// reviewer ever finds a credential in this store, the bug is that some code
// path reached the settings store with one, not that AsyncStorage leaked it.
//
// **Enumeration is why the settings store CAN list and the secret store
// cannot.** `getAllKeys()` exists here, so `SettingsStore.keys()` -- and
// therefore `AccountStore.list()` -- is a real read. The Keychain has no such
// call, which is why `secure-store.js` probes its two known item names instead
// (`expo-secure-store.js` says so where it explains the missing `keys()`).
//
// Like its counterpart, this is a function of the module rather than an import
// of it: the SDK is not installed in this environment and no test here may need
// a device (Global Constraint 10). `mobile/index.js` passes the real module.

/** The refusal a caller sees when the module it passed cannot hold settings. */
const MISSING = (what) =>
  new TypeError(`async-storage module is missing ${what}; refusing to read or write settings`);

/**
 * Wrap an `AsyncStorage` module as the port `SettingsStore` takes: the four
 * async methods, with `keys()` answering the raw key list (the settings store
 * filters it to its own namespace, so a store that shared the module with
 * somebody else's keys would still only ever see `fugaro.settings.*`).
 */
export function asyncStoragePort(AsyncStorage) {
  if (AsyncStorage === null || typeof AsyncStorage !== 'object') {
    throw MISSING('the module itself');
  }
  for (const method of ['getItem', 'setItem', 'removeItem', 'getAllKeys']) {
    if (typeof AsyncStorage[method] !== 'function') throw MISSING(method);
  }
  return {
    async getItem(key) {
      const value = await AsyncStorage.getItem(key);
      return value === undefined ? null : value;
    },
    async setItem(key, value) {
      await AsyncStorage.setItem(key, value);
    },
    /** AsyncStorage calls this `removeItem`; the port calls it `deleteItem`. */
    async deleteItem(key) {
      await AsyncStorage.removeItem(key);
    },
    async keys() {
      const all = await AsyncStorage.getAllKeys();
      return all === null || all === undefined ? [] : [...all];
    },
  };
}
