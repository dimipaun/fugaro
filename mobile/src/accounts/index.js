// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The account store's one door (mobile plan, Task P3.1; D111, D114, D125).
//
// Everything the app needs from this directory, re-exported here and nowhere
// else, so that a screen imports `src/accounts/index.js` and gets the store --
// not `secure-store.js`'s `SecretStore`, which takes a port and could be given
// the wrong one. The boundary scan (`src/boundary.test.js`) counts on the same
// arrangement from the other side: the files that may write a credential are
// two, they are named there, and a third one has to be added to that list
// deliberately.
//
// **What a caller may do with a `Secret`.** Exactly two things: hand it to
// `AccountStore.setSecret`, or call `reveal()` on one that came back from
// `AccountStore.secret` -- at the moment it is used (a token attached to a
// request, a key handed to the MAC verifier), never into a variable that
// outlives the call, and never into anything that prints. `secret.js` is what
// makes the second half of that true rather than aspirational: the value
// cannot be printed, serialised, spread or enumerated, and `reveal()` is a
// word a reviewer and a grep can both find.
//
// What is deliberately NOT exported here: the ports (`expo-secure-store.js`,
// `expo-async-storage.js`). `mobile/index.js` -- the entry, the one file that
// knows what a device is -- builds them and passes them in; a screen that
// imported one could build a second store, which is the "no shared token
// store" half of D114 undone by an import.

export {
  ACCOUNT_FIELDS,
  ACCOUNT_ID_MAX,
  ACCOUNT_TEXT_MAX,
  ACCOUNT_CODES,
  isAccountId,
  sameInstallation,
  validateAccount,
} from './account.js';

export { SECRET_ITEMS, SECRET_VALUE_MAX, SECRET_CODES, Secret, isSecret, isSecretItem, secret } from './secret.js';

export { SECRET_KEY_PREFIX, SECURE_STORE_CODES, MemorySecureStore, SecretStore, secretKey } from './secure-store.js';

export {
  SETTINGS_KEY_PREFIX,
  SETTINGS_KEY_MAX,
  SETTINGS_CODES,
  MemorySettingsStore,
  SettingsStore,
  isSettingsKey,
  secretShaped,
} from './settings.js';

export { CACHE_PREFIX, RESOURCE_CODES, AccountCache, ResourceRegistry, cacheNamespace } from './resources.js';

export { ACCOUNT_KEY_PREFIX, STORE_CODES, AccountStore } from './store.js';
