// The Keychain port: `expo-secure-store`, behind the three methods
// `secure-store.js` calls (mobile plan, Task P3.1, decision D111).
//
// **Where the phone's secrets actually live.** D111 puts both items in
// "**platform secure storage (Keychain / Keystore), and nowhere else**", and
// this file is the only place in the tree that talks to it. On iOS that is the
// Keychain; on Android it is a Keystore-backed encrypted store
// (`expo-secure-store` uses SharedPreferences + AES in the Keystore). It is not
// `AsyncStorage`, it is not a file, it is not the app's SQLite: see
// `expo-async-storage.js` for the store that holds the NON-secret side, and
// note that its header is the reason the fences in `settings.js` are
// load-bearing rather than decorative.
//
// **The one security decision this file makes.** An item is written with
// `keychainAccessible: WHEN_UNLOCKED_THIS_DEVICE_ONLY`, taken from the module
// itself rather than hardcoded:
//
//   * `THIS_DEVICE_ONLY` keeps a credential out of an iCloud Keychain backup
//     and off a restored-to-another-phone device. A token that follows the
//     owner's backup onto a second handset is a token on a device the owner
//     never paired -- D111's per-pairing MAC key is `per-pairing` in name only
//     if it can be restored elsewhere, and the MAC proves possession of the
//     key, so restoring it everywhere restores the ability to speak as this
//     installation;
//   * `WHEN_UNLOCKED` (rather than `ALWAYS` or `AFTER_FIRST_UNLOCK`) means the
//     item is not readable while the phone is locked. The app reads its items
//     when it comes to the foreground, so nothing here needs the weaker mode.
//
// `requireAuthentication` (Face ID on every read) is deliberately NOT set: the
// ID token is refreshed and read in the background, and a prompt per refresh is
// a prompt the owner would switch off -- the fence that gets switched off is
// worse than the one that was never there. The device's own lock is the
// boundary, and `THIS_DEVICE_ONLY` is what keeps the item inside it.
//
// **Why this is a function of the module and not an import of it.** Nothing in
// this tree can `import 'expo-secure-store'`: the SDK is not installed here and
// Global Constraint 10 forbids a test that needs a device. So the adapter takes
// the module as an argument -- `mobile/index.js` passes the real one -- and the
// shape of the calls it makes is tested in `expo-secure-store.test.js` against
// a fake SDK (`shared/presence`'s convention: fakes, not mocks of a device). The
// SDK's own behaviour is not exercised by anything in this repository, and
// `mobile/README.md` lists that under what is not verified.

/** The refusal a caller sees when the module it passed cannot hold an item. */
const MISSING = (what) =>
  new TypeError(`expo-secure-store module is missing ${what}; refusing to store a credential`);

/**
 * Wrap an `expo-secure-store` module as the port `SecretStore` takes.
 *
 * Fail closed at construction: a module without the `THIS_DEVICE_ONLY`
 * constant, or without the three async methods, is refused here rather than
 * silently storing an item with the platform's default accessibility (which on
 * iOS is `WHEN_UNLOCKED` and IS backed up to iCloud). That default is exactly
 * the outcome this file exists to prevent, and it is the default a typo would
 * get you.
 */
export function expoSecureStorePort(SecureStore) {
  if (SecureStore === null || typeof SecureStore !== 'object') {
    throw MISSING('the module itself');
  }
  const accessible = SecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY;
  if (accessible === undefined || accessible === null) {
    throw MISSING('WHEN_UNLOCKED_THIS_DEVICE_ONLY');
  }
  for (const method of ['getItemAsync', 'setItemAsync', 'deleteItemAsync']) {
    if (typeof SecureStore[method] !== 'function') throw MISSING(method);
  }
  return {
    /** The accessibility this port writes with, exposed so a test (and a
     * reviewer) can read the one decision above without inferring it from a
     * call it has to intercept. */
    keychainAccessible: accessible,

    async getItem(key) {
      // No options: accessibility is an attribute set when the item is
      // written, and a read addresses the item by key under the module's
      // default service -- which is the service every write here used.
      return await SecureStore.getItemAsync(key);
    },
    async setItem(key, value) {
      await SecureStore.setItemAsync(key, value, { keychainAccessible: accessible });
    },
    async deleteItem(key) {
      await SecureStore.deleteItemAsync(key);
    },
  };
  // Note there is no `keys()`. The platform cannot enumerate the Keychain, and
  // `secure-store.js` never asks it to: it probes the item names D111 declares.
  // A `keys()` here would be a promise the device cannot keep.
}
