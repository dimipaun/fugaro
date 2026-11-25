// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The one file in the spike that names `expo-secure-store` (task M0; Check
// 733's U10 and U5, and the plan's M3 and K3).
//
// **Why one file.** The repo's boundary scan (`mobile/src/boundary.test.js`)
// walks **every** `.js` under `mobile/` -- this spike included -- and holds the
// rule that the Keychain SDK's own `*ItemAsync(` surface is reached from
// `src/accounts/expo-secure-store.js` and nowhere else, "the Keychain is
// reached through one module" (`a direct SDK call bypasses the port, the
// Secret check and the accessibility`). The spike's store is a second module
// and says so where the rule is written: `spike/src/secure/sdk.js` is on that
// allow-list, in `mobile/src/boundary.test.js`, so the SDK is called here under
// its own name rather than through a spelling the pattern happens to miss. The
// rule's *intent* is kept exactly: this file is the spike's only door to the
// platform store, its one decision (the accessibility level) is taken from
// `store.js` where a test reads it, and no screen ever sees the SDK.
//
// **The undefined guard, and the crash it comes from.** `writeOptions` names an
// accessibility level; the SDK wants that level's *value*, which is the
// exported constant of the same name. On Android `expo-secure-store` takes no
// accessibility level at all, and `mobile/README.md` records the launch crash a
// wrong guess about that cost on 2026-10-21. So a level the SDK does not export
// is refused here, by name, before anything is written -- never passed through
// as `undefined` and never silently dropped.

import * as SecureStore from 'expo-secure-store';
import { Platform } from 'react-native';

import { ACCESSIBLE, writeOptions } from './store.js';

/** The accessibility level the probe writes with, as the SDK spells it. */
function accessible() {
  const name = ACCESSIBLE[Platform.OS];
  if (name === undefined || name === null) return { name: null, value: null };
  const value = SecureStore[name];
  if (typeof value !== 'string') {
    // The guard the 2026-10-21 crash is the reason for: `undefined` here would
    // be a write the platform is asked to make with no accessibility level at
    // all, which is a different write and not a smaller one.
    throw new Error(`expo-secure-store exports no accessibility level named ${name}`);
  }
  return { name, value };
}

/** The options one write is made with: the level's value, or nothing on a platform that takes none. */
function writeOptionsForPlatform() {
  const named = writeOptions(Platform.OS);
  if (!Object.prototype.hasOwnProperty.call(named, 'keychainAccessible')) return {};
  return { keychainAccessible: accessible().value };
}

/**
 * The spike's store port: the four things the screens are allowed to do with
 * the platform's secure storage. It is a port and not the SDK so that a screen
 * cannot reach a fifth thing, and so that the accessibility decision is made
 * once, here, from `store.js`.
 */
export function createSecureStore() {
  return {
    /** A read-back of one item, or null when there is none. The value is the caller's to judge, never to print. */
    async read(key) {
      return (await SecureStore.getItemAsync(key)) ?? null;
    },
    /** Write one item with the level `store.js` names for this platform. */
    async put(key, value) {
      await SecureStore.setItemAsync(key, String(value), writeOptionsForPlatform());
    },
    /** Remove one item, so a probe can be taken back off the handset. */
    async remove(key) {
      await SecureStore.deleteItemAsync(key);
    },
    /** The level this platform's writes carry, for the U10 screen to show. */
    accessible: accessible(),
  };
}
