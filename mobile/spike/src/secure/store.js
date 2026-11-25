// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The secure-store probe U10 is read from (task M0; Check 733's U10; the
// reconciliation's U10, and the plan's M3 and K3).
//
// **What U10 asks.** Whether the platform's **synced** keychain works with the
// secure-store option the app uses -- the property restore-from-the-synced-list
// rests on (M3, K3). The app's own store (`mobile/src/accounts/expo-secure-store.js`)
// writes every item with `WHEN_UNLOCKED_THIS_DEVICE_ONLY`, and that option is
// the opposite of a synced one: `THIS_DEVICE_ONLY` is the accessibility level
// that keeps an item **out of an iCloud Keychain backup and off a device a
// backup is restored onto**. So the question is not "does the keychain sync" --
// it is "which option makes the item sync, and does the SDK offer one".
//
// **The two things this file knows about, and which of them is a fact.**
//
//   * **A fact of the SDK's option list.** `expo-secure-store` exports
//     `WHEN_UNLOCKED`, `WHEN_UNLOCKED_THIS_DEVICE_ONLY`,
//     `AFTER_FIRST_UNLOCK`, `AFTER_FIRST_UNLOCK_THIS_DEVICE_ONLY` and
//     `WHEN_PASSCODE_SET_THIS_DEVICE_ONLY`. Those are the accessibility
//     *levels*, and none of them is a sync switch: iCloud Keychain
//     synchronisation is `kSecAttrSynchronizable`, a separate attribute that
//     `expo-secure-store` does not expose at all. Writing with `WHEN_UNLOCKED`
//     (no `THIS_DEVICE_ONLY`) is the closest this stack gets: it makes the item
//     eligible for an encrypted backup, and the owner's reading on a second
//     phone is what settles whether it appears there.
//   * **A reading only a device can give.** Whether the item turns up on a
//     second phone signed into the same Apple ID. `README.md` has the steps;
//     the U10 screen has the field the reading is written into.
//
// **What this file does not do.** It does not claim a sync it cannot see, and
// it does not write the app's own items: the spike's namespace is its own
// (`fugaro.spike.`), so a probe item can never collide with -- or be mistaken
// for -- a paired device's material.

/** The spike's own namespace, which is deliberately not the app's (`fugaro.account.`). */
export const ITEM_PREFIX = 'fugaro.spike.';

/** The item U10 writes, and the item U5 writes, as the two the screens read back. */
export const ITEM_NAMES = Object.freeze({
  sync: 'u10-keychain-probe',
  deviceKey: 'u5-software-key',
});

/**
 * The key one item is written under. The prefix is composed **here and nowhere
 * else**, so the probe's namespace has one author; a name outside the set above
 * is refused rather than composed, which is what keeps a stray string out of
 * the store.
 */
export function itemKey(name) {
  if (!Object.values(ITEM_NAMES).includes(name)) {
    throw new Error(`${JSON.stringify(name)} is not one of this spike's items: ${Object.values(ITEM_NAMES).join(', ')}`);
  }
  return `${ITEM_PREFIX}${name}`;
}

/** The accessibility level the read-back is asked with, per platform. */
export const ACCESSIBLE = Object.freeze({
  /** iOS: the item may ride a backup; `THIS_DEVICE_ONLY` may not, and that is the probe. */
  ios: 'WHEN_UNLOCKED',
  /** Android: `expo-secure-store`'s Android side takes no accessibility level at all. */
  android: null,
});

/** The options one write is made with, per platform -- what the screen shows. */
export function writeOptions(platform) {
  if (platform === 'ios') return { keychainAccessible: ACCESSIBLE.ios };
  return {};
}

/**
 * What the SDK offers, as the screen states it. The list is the SDK's own
 * exported names (a constant per level), and `synchronizable: false` is the
 * finding: **no exported option turns iCloud Keychain synchronisation on**, so
 * a synced item through this SDK is not a setting -- it does not exist.
 */
export const KEYCHAIN_OPTIONS = Object.freeze({
  levels: Object.freeze([
    'WHEN_UNLOCKED',
    'WHEN_UNLOCKED_THIS_DEVICE_ONLY',
    'AFTER_FIRST_UNLOCK',
    'AFTER_FIRST_UNLOCK_THIS_DEVICE_ONLY',
    'WHEN_PASSCODE_SET_THIS_DEVICE_ONLY',
  ]),
  synchronizable: false,
  why:
    'the SDK exposes accessibility levels and no kSecAttrSynchronizable attribute, so WHEN_UNLOCKED (without ' +
    'THIS_DEVICE_ONLY) is the closest this stack gets to an item that can leave the phone',
});

/**
 * What U10 means for the plan, as one sentence the screen prints and the draft
 * quotes. It is written here rather than in the screen so the two cannot drift.
 */
export const RESTORE_MEANING =
  'restore-from-the-synced-list (M3, K3) cannot rest on an iCloud-synced keychain through expo-secure-store: ' +
  'the SDK has no synchronizable option, so a second phone reads nothing unless the item is restored from an ' +
  'encrypted backup, which is the behaviour U10 is read for.';

/**
 * A read-back, as the screen shows it: whether the item is there, how long the
 * value is, and -- never -- the value itself. The probe item holds a length and
 * a timestamp, not a secret; but the *shape* is the one every read-back in this
 * project has, so a screen cannot get into the habit of printing a stored value.
 */
export function readBack({ value, expected }) {
  if (value === undefined || value === null) return { present: false, length: 0, matches: false };
  return { present: true, length: String(value).length, matches: value === expected };
}
