// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The demo's key source: the per-pairing key the demo's client signs and
// verifies with, in memory and nowhere else (owner ruling of 2026-10-19).
//
// **Why the demo needs a key at all.** The app's client refuses to sign
// anything without one and drops every message it cannot verify (P3.6), so a
// demo with no key would show a client that refuses everything -- it would
// demonstrate the refusals and none of the behaviour. The key here is D171's
// per-pairing key in *shape* only: a placeholder string, this module's own,
// that opens nothing anywhere.
//
// **What it never does, and the test that holds it.** It does not import, read,
// construct or fall back to the platform's secure storage: P3.1's `SecretStore`
// is not built here, `expo-secure-store` is not imported anywhere under
// `src/demo/`, and `safety.test.js` asserts both -- that entering demo mode
// against the app's real `SecretStore` and `AccountStore` touches neither (the
// Keychain port's every method is instrumented and sees no call), and that no
// file in this directory so much as names the storage SDK.
//
// **The item name is D111's.** `use` answers for exactly the one item
// `client.js` asks for (`MAC_KEY_ITEM`) and refuses any other with P3.1's own
// code, so a later caller cannot read something out of a demo key source that
// does not exist. The refusal codes are P3.1's rather than this module's,
// because the client already knows how to read them (`absent` is "not paired",
// everything else is "unreadable").

import { SECURE_STORE_CODES } from '../accounts/index.js';

/** D111's item name for the per-pairing key, spelled as `client.js` spells it. */
export const DEMO_KEY_ITEM = 'macKey';

/**
 * The demo's pairing key: a placeholder, obviously not a key, and asserted to
 * be one by this module's tests. It is long enough to be a real key's shape
 * (so nothing about the demo's sizes is a lie) and carries the word `demo` in
 * its first characters, so a reviewer who finds it in a log, a screenshot or a
 * fixture sees what it is.
 */
export const DEMO_PAIRING_KEY = 'demo-pairing-key-not-a-real-key-0000000000000001';

/**
 * The demo's key source, in the shape the client takes a `SecretStore` in --
 * `use(accountId, item, fn)`, the one method P3.6 added to P3.1's store for
 * exactly this: the material is handed to a callback, used inside it, and
 * never returned to the caller.
 *
 * It is **not** a `SecretStore` and must not become one: a `SecretStore` takes
 * a platform port, and a demo that took one would be a demo that could read a
 * phone's Keychain.
 *
 * @returns {{ use: (accountId: string, item: string, fn: (key: string) => unknown) => Promise<object> }}
 */
export function demoKeySource() {
  return Object.freeze({
    /**
     * @param {unknown} accountId the account the caller thinks it is using; the
     *   demo has one key and answers for it, and the argument is deliberately
     *   not used to look anything up
     * @param {unknown} item the item asked for; only D111's `macKey` is answered
     * @param {unknown} fn the callback the material is handed to for one call
     */
    async use(accountId, item, fn) {
      if (typeof fn !== 'function') {
        return Object.freeze({ ok: false, code: SECURE_STORE_CODES.NOT_A_CALLBACK, field: 'fn' });
      }
      if (item !== DEMO_KEY_ITEM) {
        return Object.freeze({ ok: false, code: SECURE_STORE_CODES.UNKNOWN_ITEM, field: 'item' });
      }
      try {
        return Object.freeze({ ok: true, result: await fn(DEMO_PAIRING_KEY) });
      } catch {
        // A callback that threw is P3.1's `callback-failed`, and what it threw
        // is never carried: it may quote the material (the rule `use` exists
        // for).
        return Object.freeze({ ok: false, code: SECURE_STORE_CODES.CALLBACK_FAILED, field: 'fn' });
      }
    },
  });
}
