// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// What the app may keep outside secure storage (Task P3.1; D111, D114).
//
// D111's list is a statement about **where a credential lives**, and the half
// of it that is easy to get wrong is the negative one: "both never logged,
// **never in a settings file**". The app does keep settings -- D114 makes the
// account list "a list the owner maintains, not a discovery result", so the
// installation tuples have to be written down somewhere -- and console D13's
// precedent says exactly what may be written there: **"the app stores the
// tuples, never a credential"**, and Voice D42(2) says a document "may hold a
// keychain item's name or a provider's name, never a value".
//
// So this module is a settings store with the rule built into it, in three
// fences, because a comment does not refuse anything:
//
//   * **a key may not be, or end in, one of D111's item names**, and may not
//     carry the prefix the secure store composes its keys from -- so a setting
//     can never collide with, or masquerade as, an item;
//   * **a value must be a plain JSON value**. A `Secret`, or any other class
//     instance, is refused rather than serialised, which is what stops the
//     obvious mistake (`settings.set('token', secretValue)`) at the one door
//     it would arrive at, whatever the secret's shape;
//   * **a value must not be secret-shaped**: the credential classes this tree
//     actually uses have known shapes, and a string matching one is refused.
//
// **What the shapes can and cannot do, said plainly.** A shape list recognises
// what it has been taught: a JWT, a PEM block, a Google API key, an `sk-`
// model key, a long hex string. It cannot recognise a base64url MAC key that
// is merely a long run of letters, because that is also what an ordinary long
// project name looks like -- and refusing every long string would refuse a
// legitimate tuple, which is how a fence gets deleted by the next person who
// trips over it. The two fences that do not depend on shape are the ones that
// actually hold: the **type** fence above (a `Secret` can never be a settings
// value, whatever it holds) and the **boundary scan** in `src/boundary.test.js`
// (no file under `mobile/` may call `reveal()` outside an allow-list, so a
// revealed value has to be written down in a word a reviewer can see). This
// module does not pretend the shape list is the whole fence.

import { isAccountId } from './account.js';
import { SECRET_ITEMS } from './secret.js';

/** The prefix a settings key is stored under. */
export const SETTINGS_KEY_PREFIX = 'fugaro.settings.';

/**
 * The key an account's tuple is stored under, `<this><id>`. It lives here
 * rather than in `store.js` because it is a SETTINGS KEY and the fence that
 * judges settings keys has to know it; `store.js` imports it from here and the
 * module's door re-exports it, so callers see one name and one value.
 */
export const ACCOUNT_KEY_PREFIX = 'account.';

/** The longest settings key, in code units. */
export const SETTINGS_KEY_MAX = 128;

/** The refusal codes this module returns. */
export const SETTINGS_CODES = Object.freeze({
  BAD_KEY: 'bad-key',
  RESERVED_KEY: 'reserved-key',
  SECRET_KEY: 'secret-key',
  SECRET_VALUE: 'secret-value',
  UNREADABLE_VALUE: 'unreadable-value',
  ABSENT: 'absent',
  STORE_FAILED: 'store-failed',
});

/**
 * The segment names a settings key may not use, matched on the whole segment:
 * an exact match, so `firebaseProjectId` is not caught by `id` and no
 * legitimate field of a tuple is refused. This is the "a name, never a value"
 * rule turned into a list of names.
 *
 * D111's item names are DERIVED at the bottom of the list rather than repeated
 * here, so a third item added to `SECRET_ITEMS` is fenced the moment it is
 * declared -- and `settings.test.js` asserts that property for each of them,
 * so removing the derivation is red rather than silent.
 *
 * The list applies to keys that are NOT account keys (`#judgeKey` says why: an
 * account id is the owner's own word). It is exported so that the test which
 * walks every word as a candidate account id can ask for the list instead of
 * repeating it -- the same one-list-in-one-place rule as `SECRET_ITEMS` above.
 *
 * It deliberately does NOT contain `mac`, even though `mac` is a name an owner
 * might give an installation: the alphabet rule already keeps account ids out
 * of this list's way, and `macKey` -- the name that matters -- is here,
 * derived.
 */
export const FORBIDDEN_KEY_SEGMENTS = Object.freeze([
  'token',
  'secret',
  'password',
  'credentials',
  'credential',
  'privatekey',
  'apikey',
  ...SECRET_ITEMS.map((item) => item.toLowerCase()),
]);

/** The prefix the secure store owns. A settings key may not start with it. */
const SECRET_PREFIX = 'fugaro.account.';

const SETTINGS_KEY_RE = new RegExp(`^[A-Za-z0-9._-]{1,${SETTINGS_KEY_MAX}}$`);

/**
 * The credential shapes this tree uses, as data so the list can be read and
 * extended in one place. Each entry is a reason string and its pattern.
 */
const SECRET_SHAPES = Object.freeze([
  ['jwt', /^eyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}$/],
  ['pem-private-key', /-----BEGIN [A-Z ]*PRIVATE KEY-----/],
  ['pem-certificate', /-----BEGIN [A-Z ]*CERTIFICATE-----/],
  ['google-api-key', /^AIza[0-9A-Za-z_-]{35}$/],
  ['model-api-key', /^sk-[A-Za-z0-9_-]{16,}$/],
  ['long-hex', /^[0-9a-fA-F]{32,}$/],
]);

/**
 * Does this string look like a credential? Returns the reason, or `null`.
 *
 * Exported so that a test (and `app-config.test.js`, which reads `app.json`)
 * can ask the same question this store asks, rather than a second list that
 * could disagree with it.
 */
export function secretShaped(value) {
  if (typeof value !== 'string') return null;
  for (const [reason, pattern] of SECRET_SHAPES) {
    if (pattern.test(value)) return reason;
  }
  return null;
}

/** Is `key` usable as a settings key? */
export function isSettingsKey(key) {
  return typeof key === 'string' && SETTINGS_KEY_RE.test(key);
}

/**
 * Walk a candidate value and refuse anything that may not be written down.
 *
 * Returns `null` when the value is a plain JSON value, or `{ code, path }`.
 * Arrays and plain objects are walked; a class instance is `secret-value`
 * whatever it is (a `Secret` is the one that matters, but a future
 * `AccessToken` class would be caught by the same rule rather than by a list
 * someone has to remember to extend); a non-finite number is refused because
 * `JSON.stringify` silently turns it into `null`, and a value that changes on
 * the way to disk is a value this store is not allowed to keep.
 */
function judge(value, path) {
  switch (typeof value) {
    case 'string':
      return secretShaped(value) === null ? null : { code: SETTINGS_CODES.SECRET_VALUE, path };
    case 'boolean':
      return null;
    case 'number':
      return Number.isFinite(value) ? null : { code: SETTINGS_CODES.UNREADABLE_VALUE, path };
    case 'object': {
      if (value === null) return null;
      if (Array.isArray(value)) {
        for (let i = 0; i < value.length; i++) {
          const bad = judge(value[i], `${path}[${i}]`);
          if (bad !== null) return bad;
        }
        return null;
      }
      if (Object.getPrototypeOf(value) !== Object.prototype) {
        // A `Secret`, a Map, a Date, anything with a prototype of its own.
        return { code: SETTINGS_CODES.SECRET_VALUE, path };
      }
      for (const [key, nested] of Object.entries(value)) {
        if (FORBIDDEN_KEY_SEGMENTS.includes(key.toLowerCase())) {
          return { code: SETTINGS_CODES.SECRET_VALUE, path: `${path}.${key}` };
        }
        const bad = judge(nested, `${path}.${key}`);
        if (bad !== null) return bad;
      }
      return null;
    }
    default:
      // undefined, function, symbol, bigint: none of them survives a JSON
      // round trip unchanged, and a setting that comes back as something else
      // is worse than one that was refused.
      return { code: SETTINGS_CODES.UNREADABLE_VALUE, path };
  }
}

/** A frozen refusal, in the shape the shared package's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * The platform port's in-memory double: JSON strings behind an async
 * key/value API, with `keys()` for the tests. On a device this port is backed
 * by the app's own persistent storage; nothing here is a device.
 */
export class MemorySettingsStore {
  #items = new Map();

  async getItem(key) {
    return this.#items.has(key) ? this.#items.get(key) : null;
  }

  async setItem(key, value) {
    this.#items.set(key, value);
  }

  async deleteItem(key) {
    this.#items.delete(key);
  }

  /** The raw key set. Tests only. */
  keys() {
    return [...this.#items.keys()].sort();
  }
}

/**
 * The settings store, with the three fences above applied to every write.
 *
 * A caller's key is namespaced under `fugaro.settings.`, so a raw port that
 * also held something else could not be reached through this store by naming
 * it, and the secure store's own prefix is refused outright.
 */
export class SettingsStore {
  #port;

  constructor(port) {
    if (port === null || typeof port !== 'object') throw new TypeError('a settings port is required');
    this.#port = port;
  }

  /** The raw key a caller's key is stored under. */
  static rawKey(key) {
    return `${SETTINGS_KEY_PREFIX}${key}`;
  }

  /**
   * Judge a key. `reserved-key` is the secure store's namespace, `secret-key`
   * is a key whose segment names one of the forbidden words (D111's item names
   * among them -- that list is where the coupling lives), and `bad-key` is
   * everything else that is not a key.
   *
   * **An account key is judged as a key, not by its words.** `account.<id>` is
   * the account store's own key, and `<id>` is a word the OWNER chose: a
   * segment-by-segment check against the word list refused an installation
   * called `token`, `secret`, `password`, `apiKey`, `idToken`, `macKey` and the
   * rest -- the same "fence that refused the owner's own installation" fault as
   * `mac`, one word short of the fix. So the account branch is separate and
   * stricter: the remainder after `account.` must be a valid account id
   * (`isAccountId`, whose alphabet has no `.` in it) and nothing else. That
   * admits every name the owner can type, and refuses `account.<id>.token` --
   * a smuggled name -- as a key this store does not own.
   *
   * The word list still protects everything else: a key that is not an account
   * key may not use one of the words, and the VALUE fences (`Secret`, shape)
   * apply to account keys exactly as to any other.
   */
  #judgeKey(key) {
    if (!isSettingsKey(key)) return refuse(SETTINGS_CODES.BAD_KEY, 'key');
    if (key.startsWith(SECRET_PREFIX)) return refuse(SETTINGS_CODES.RESERVED_KEY, 'key');
    if (key.startsWith(ACCOUNT_KEY_PREFIX)) {
      const accountId = key.slice(ACCOUNT_KEY_PREFIX.length);
      if (!isAccountId(accountId)) return refuse(SETTINGS_CODES.BAD_KEY, 'key');
      return null;
    }
    const segments = key.toLowerCase().split('.');
    for (const segment of segments) {
      if (FORBIDDEN_KEY_SEGMENTS.includes(segment)) {
        return refuse(SETTINGS_CODES.SECRET_KEY, 'key');
      }
    }
    return null;
  }

  async set(key, value) {
    const badKey = this.#judgeKey(key);
    if (badKey !== null) return badKey;
    const bad = judge(value, 'value');
    if (bad !== null) return refuse(bad.code, bad.path);
    try {
      await this.#port.setItem(SettingsStore.rawKey(key), JSON.stringify(value));
    } catch {
      return refuse(SETTINGS_CODES.STORE_FAILED, 'value');
    }
    return Object.freeze({ ok: true });
  }

  async get(key) {
    const badKey = this.#judgeKey(key);
    if (badKey !== null) return badKey;
    let stored;
    try {
      stored = await this.#port.getItem(SettingsStore.rawKey(key));
    } catch {
      return refuse(SETTINGS_CODES.STORE_FAILED, 'key');
    }
    if (stored === null || stored === undefined) return refuse(SETTINGS_CODES.ABSENT, 'key');
    let value;
    try {
      value = JSON.parse(stored);
    } catch {
      return refuse(SETTINGS_CODES.UNREADABLE_VALUE, 'key');
    }
    // The same rule on the way back: a value that could not have been written
    // through this store is refused rather than handed to a caller that would
    // treat it as settings.
    const bad = judge(value, 'value');
    if (bad !== null) return refuse(SETTINGS_CODES.UNREADABLE_VALUE, 'key');
    return Object.freeze({ ok: true, value });
  }

  /** Remove one setting. Absent is not an error: it is already gone. */
  async delete(key) {
    const badKey = this.#judgeKey(key);
    if (badKey !== null) return badKey;
    try {
      await this.#port.deleteItem(SettingsStore.rawKey(key));
    } catch {
      return refuse(SETTINGS_CODES.STORE_FAILED, 'key');
    }
    return Object.freeze({ ok: true });
  }

  /** Every caller key this store holds, in order, with no value read. */
  async keys() {
    let raw;
    try {
      raw = await this.#port.keys();
    } catch {
      return refuse(SETTINGS_CODES.STORE_FAILED, 'key');
    }
    const keys = raw
      .filter((key) => key.startsWith(SETTINGS_KEY_PREFIX))
      .map((key) => key.slice(SETTINGS_KEY_PREFIX.length))
      .sort();
    return Object.freeze({ ok: true, keys: Object.freeze(keys) });
  }
}
