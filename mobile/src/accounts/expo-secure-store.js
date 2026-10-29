// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
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
//
// **The platform is an argument because the accessibility levels are iOS's
// (added 2026-10-21, the Android launch crash).** This port used to require
// `SecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY` unconditionally, and that
// constant **does not exist on Android**. It is declared by the Swift module
// only: `expo-secure-store`'s `ios/SecureStoreModule.swift` registers seven
// `Constant(...)` entries, and `android/.../SecureStoreModule.kt` registers
// **none**, so the SDK's own
// `export const WHEN_UNLOCKED_THIS_DEVICE_ONLY = ExpoSecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY`
// is `undefined` on Android. The failure was therefore not a bad value but a
// **throw while `mobile/index.js` was still being evaluated** -- the port is
// built at module scope, before `registerRootComponent` and long before React
// renders -- so an Android build installed, opened and died with no screen at
// all. That is the owner's report of 2026-10-21 against the `preview` APK, and
// the APK was otherwise correct: `expo-secure-store` 58.0.2 is the SDK 58
// version `npx expo install` picks, its config plugin is in `app.json`, and the
// module is linked and works on Android -- it simply has no accessibility
// levels to name, and its `SecureStoreOptions` record has no `keychainAccessible`
// field to receive one.
//
// So the rule is now the platform's and not the module's:
//
//   * **iOS**: the constant is required, exactly as before. A renamed constant
//     is a loud refusal rather than a silent downgrade to the platform's
//     default (`WHEN_UNLOCKED`), which is the item that rides an iCloud backup
//     onto another phone.
//   * **Android**: no constant is asked for, no `keychainAccessible` is sent,
//     and the port is built. The Keystore-backed store has no such attribute.
//
// The one thing that would be wrong is reading the rule off the *module* ("send
// the option when the module happens to define it"): on iOS that turns a rename
// into a silent downgrade, which is the outcome the paragraph above exists to
// prevent. The platform is a fact `mobile/index.js` knows (`Platform.OS`) and
// `src/` must not import react-native to learn, which is why it arrives as
// `options.platform`. An unknown platform is refused -- and that refusal is safe
// to keep loud because the entry builds this port inside the guarded startup
// (`src/app/startup.js`), which renders a readable screen instead of closing.

/** The refusal a caller sees when the module it passed cannot hold an item. */
const MISSING = (what) =>
  new TypeError(`expo-secure-store module is missing ${what}; refusing to store a credential`);

/**
 * The platforms this port knows, and the reason each is a decision: `ios` is
 * the one with Keychain accessibility levels, `android` is the one whose store
 * has none. There is no third: `app.json` builds for these two, and a platform
 * nobody has thought about is refused rather than guessed at.
 */
export const PORT_PLATFORMS = Object.freeze({ IOS: 'ios', ANDROID: 'android' });

/** The refusal a caller sees when it did not say which platform this is. */
const NO_PLATFORM = (platform) =>
  new TypeError(
    `expo-secure-store port: the platform must be "${PORT_PLATFORMS.IOS}" or ` +
      `"${PORT_PLATFORMS.ANDROID}", not ${JSON.stringify(platform)}. Which platform this is decides ` +
      `whether the iOS-only keychain accessibility constant is required, so it is asked for ` +
      `rather than assumed; mobile/index.js passes Platform.OS.`,
  );

/**
 * Wrap an `expo-secure-store` module as the port `SecretStore` takes.
 *
 * Fail closed at construction **for the platform it is told about**: on iOS, a
 * module without the `THIS_DEVICE_ONLY` constant, or any module without the
 * three async methods, is refused here rather than silently storing an item
 * with the platform's default accessibility (which on iOS is `WHEN_UNLOCKED`
 * and IS backed up to iCloud). That default is exactly the outcome this file
 * exists to prevent, and it is the default a typo would get you. On Android the
 * constant is not asked for, because Android has none (see the header).
 *
 * @param {object} SecureStore the `expo-secure-store` module
 * @param {{ platform: string }} options `platform` is `Platform.OS`: `"ios"`
 *   or `"android"`. Required -- the accessibility rule is a function of it.
 * @returns {object} the port, with `platform` and `keychainAccessible` exposed
 *   so a test (and a reviewer) can read the decision above rather than infer it
 *   from a call it has to intercept. `keychainAccessible` is `null` on a
 *   platform that has none.
 */
export function expoSecureStorePort(SecureStore, options = {}) {
  const platform = options === null || typeof options !== 'object' ? undefined : options.platform;
  if (platform !== PORT_PLATFORMS.IOS && platform !== PORT_PLATFORMS.ANDROID) {
    throw NO_PLATFORM(platform);
  }
  if (SecureStore === null || typeof SecureStore !== 'object') {
    throw MISSING('the module itself');
  }
  for (const method of ['getItemAsync', 'setItemAsync', 'deleteItemAsync']) {
    if (typeof SecureStore[method] !== 'function') throw MISSING(method);
  }
  // The iOS half of the rule, and only the iOS half: this is where the item's
  // accessibility is decided, and on iOS an absent constant must refuse the
  // port rather than write the item with the platform's default.
  let accessible = null;
  if (platform === PORT_PLATFORMS.IOS) {
    accessible = SecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY;
    if (accessible === undefined || accessible === null) {
      throw MISSING('WHEN_UNLOCKED_THIS_DEVICE_ONLY');
    }
  }
  return {
    platform,
    keychainAccessible: accessible,

    async getItem(key) {
      // No options: accessibility is an attribute set when the item is
      // written, and a read addresses the item by key under the module's
      // default service -- which is the service every write here used.
      return await SecureStore.getItemAsync(key);
    },
    async setItem(key, value) {
      // The third argument is sent only where the platform has the attribute.
      // On Android the Kotlin `SecureStoreOptions` record has no
      // `keychainAccessible` field; sending one would be sending a setting the
      // other end does not have.
      if (accessible === null) await SecureStore.setItemAsync(key, value);
      else await SecureStore.setItemAsync(key, value, { keychainAccessible: accessible });
    },
    async deleteItem(key) {
      await SecureStore.deleteItemAsync(key);
    },
  };
  // Note there is no `keys()`. The platform cannot enumerate the Keychain, and
  // `secure-store.js` never asks it to: it probes the item names D111 declares.
  // A `keys()` here would be a promise the device cannot keep.
}
