// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// Where the phone's two items live (mobile plan, Task P3.1; decision D111).
//
// D111 puts both of them -- the Firebase Auth ID token and the per-pairing MAC
// key -- in **platform secure storage (Keychain / Keystore), and nowhere
// else**. This module is the only code under `mobile/` that writes to it, and
// the boundary scan (`src/boundary.test.js`) is what holds that: no other file
// may call the platform's `setItem`/`setItemAsync`, so a secret cannot be
// written by a path a reviewer has to find by reading.
//
// **The port, and why there is one.** `expo-secure-store` is a device API:
// there is no Keychain in CI, and Global Constraint 10 forbids a test that
// needs a device. So the platform sits behind a three-method port
// (`getItem`/`setItem`/`deleteItem`, all async, all string-valued) exactly as
// Voice D52 requires fakes "from task one", and the real adapter is
// `expo-secure-store.js` -- a file with no logic to test and a `// no test
// here` note saying so. What is tested below is every decision this repository
// made about the items: which names they take, that only a `Secret` can be
// written, that a write that fails is a refusal rather than a silent success,
// and that one account's keys are not another's.
//
// **No enumeration.** The real platform cannot list keys, so this module never
// tries: it probes the two item names it knows. `MemorySecureStore.keys()`
// exists for the tests alone -- it is how a test proves the key SET, rather
// than only the values it happened to read -- and `mobile/README.md` says so
// where it describes the fakes.
//
// **The key names, and why they are composed here.** A key is
// `fugaro.account.<accountId>.<item>`. The account id is validated before it
// is composed (`isAccountId`, from `account.js`), because an id that could
// carry the separator would address an item belonging to another account --
// the one way this module could return a value it should not. The composed
// key is also the thing a settings write must never be able to collide with,
// which `settings.js` enforces by refusing the prefix.

import { isAccountId } from './account.js';
import { SECRET_ITEMS, SECRET_VALUE_MAX, isSecret, isSecretItem, secret } from './secret.js';

/** The prefix every secret key carries, and no settings key may. */
export const SECRET_KEY_PREFIX = 'fugaro.account.';

/** The refusal codes this module returns. */
export const SECURE_STORE_CODES = Object.freeze({
  BAD_ACCOUNT: 'bad-account',
  UNKNOWN_ITEM: 'unknown-item',
  NOT_A_SECRET: 'not-a-secret',
  ITEM_MISMATCH: 'item-mismatch',
  ABSENT: 'absent',
  UNREADABLE: 'unreadable',
  STORE_FAILED: 'store-failed',
  /** `use` was given something that is not a callback (P3.6). */
  NOT_A_CALLBACK: 'not-a-callback',
  /** The callback threw. What it threw is never carried: it may quote the material. */
  CALLBACK_FAILED: 'callback-failed',
  /** The callback tried to hand the material back (P3.6). See `use`. */
  LEAKED: 'leaked',
});

/** A frozen refusal, in the shape the shared package's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * The key one item of one account lives under.
 *
 * Throws on input it cannot compose: composing a key from an unchecked id is
 * how a key comes to point at another account's item, and there is no safe
 * fallback for that. Callers that take either value from outside call
 * `isAccountId`/`isSecretItem` first and refuse, as `SecretStore` does.
 */
export function secretKey(accountId, item) {
  if (!isAccountId(accountId)) throw new TypeError('not an account id');
  if (!isSecretItem(item)) throw new TypeError(`unknown secret item: ${String(item)}`);
  return `${SECRET_KEY_PREFIX}${accountId}.${item}`;
}

/**
 * The platform port's in-memory double: a `Map` behind the three async
 * methods, with a `keys()` for the tests (above). It is not a device, and it
 * is not durable -- it exists so that the account store's rules can be
 * asserted with no Keychain, no install and no network.
 */
export class MemorySecureStore {
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

  /** The exact key set. Tests only: the platform cannot enumerate. */
  keys() {
    return [...this.#items.keys()].sort();
  }
}

/**
 * The only writer of the phone's two items.
 *
 * Every method returns `{ ok: true, ... }` or a frozen refusal, and nothing is
 * coerced: a value that is not a `Secret` is refused rather than stringified,
 * so a bare string can never reach the Keychain through this door.
 */
export class SecretStore {
  #port;

  /** @param port a `getItem`/`setItem`/`deleteItem` triple (see the port above). */
  constructor(port) {
    if (port === null || typeof port !== 'object') throw new TypeError('a secure-store port is required');
    this.#port = port;
  }

  /**
   * Write one item. The value must be a `Secret` **for that item**: a MAC key
   * written under the token's name would be read back as a token, and the
   * failure would surface as an authentication error a long way from here.
   */
  async put(accountId, item, value) {
    if (!isAccountId(accountId)) return refuse(SECURE_STORE_CODES.BAD_ACCOUNT, 'accountId');
    if (!isSecretItem(item)) return refuse(SECURE_STORE_CODES.UNKNOWN_ITEM, 'item');
    if (!isSecret(value)) return refuse(SECURE_STORE_CODES.NOT_A_SECRET, 'value');
    if (value.item !== item) return refuse(SECURE_STORE_CODES.ITEM_MISMATCH, 'item');
    try {
      await this.#port.setItem(secretKey(accountId, item), value.reveal());
    } catch {
      // Fail closed and loudly: a write that did not land must not look like
      // one that did, or the caller goes on to sign with a key it does not
      // have. The platform's own error is deliberately not carried in the
      // refusal -- it may quote the value.
      return refuse(SECURE_STORE_CODES.STORE_FAILED, 'value');
    }
    return Object.freeze({ ok: true });
  }

  /**
   * Read one item back, wrapped: `{ ok: true, secret }`, or
   * `{ ok: false, code: 'absent' }` when the account has never been paired,
   * or `'unreadable'` when the platform returned something that is not a
   * value this module could have written.
   */
  async get(accountId, item) {
    if (!isAccountId(accountId)) return refuse(SECURE_STORE_CODES.BAD_ACCOUNT, 'accountId');
    if (!isSecretItem(item)) return refuse(SECURE_STORE_CODES.UNKNOWN_ITEM, 'item');
    let stored;
    try {
      stored = await this.#port.getItem(secretKey(accountId, item));
    } catch {
      return refuse(SECURE_STORE_CODES.STORE_FAILED, 'item');
    }
    if (stored === null || stored === undefined) return refuse(SECURE_STORE_CODES.ABSENT, 'item');
    // Re-apply the write's own rules to what came back. A stored value that
    // this module could not have written is not repaired into one: it is
    // refused, because "nearly the right credential" authenticates as nobody.
    const reread = secret(item, stored);
    if (!reread.ok) return refuse(SECURE_STORE_CODES.UNREADABLE, 'item');
    return Object.freeze({ ok: true, secret: reread.secret });
  }

  /**
   * Hand one item's material to a callback for the duration of one call, and
   * give back only what the callback returned.
   *
   * **Why this exists, and why it is here rather than in the caller.** The
   * per-pairing MAC key of D171 has to be used somewhere: P3.6's client signs
   * an inbox message with it and verifies the consumer's tags with it. The only
   * way to the material is `reveal()`, which `src/boundary.test.js` allows in
   * exactly two files -- `secret.js`, which defines it, and this one. A client
   * that called `reveal()` itself would be a third file, and widening that list
   * is how the fence is lost. So the reveal happens here, inside the store that
   * is already allowed to make it, and the material never becomes a value the
   * caller holds: it is an argument to the callback, and a callback cannot
   * return it by accident without being caught (below).
   *
   * **The fence against the material leaving.** The callback's result is
   * walked -- strings, arrays and plain objects, four levels deep -- and if the
   * material appears anywhere in it the answer is a `leaked` refusal rather
   * than a result. This is the "never logged, never stored in the clear" rule
   * (D111) applied to the one new path to the material: a caller that put the
   * key into the value it returned would be a caller one step from printing it.
   * The walk is a fence and not a proof, and the limit is named: material
   * inside a closure, a class instance's private field, or nested more than
   * four levels deep is not caught. A caller that wants to smuggle the key out
   * can still do it; what it cannot do is do it by accident.
   *
   * **What is a leak and what is a coincidence.** The fence answers "did the
   * material come out", not "do its characters occur", because the two are not
   * the same question for a short key: P3.6's client returns the 64-hex tag it
   * made with the key, and a two-character key is inside such a tag more often
   * than not. Material of at least `LEAK_SEARCH_MIN` characters is searched for
   * anywhere; a shorter one is only a leak when it comes back as a run of its
   * own rather than inside a longer word (`leakIn`). Both halves are tested,
   * the second one in `secure-store.test.js` and again at the client's own
   * boundary, where the false refusal would be a phone that cannot speak.
   *
   * A callback that throws is `callback-failed`, and what it threw is not
   * carried: an exception from code that held the material may quote it.
   *
   * @param {string} accountId
   * @param {string} item one of `SECRET_ITEMS`
   * @param {(material: string) => unknown} fn the use, which may be async
   * @returns {Promise<{ ok: true, result: unknown } | { ok: false, code: string, field: string | null }>}
   */
  async use(accountId, item, fn) {
    if (!isAccountId(accountId)) return refuse(SECURE_STORE_CODES.BAD_ACCOUNT, 'accountId');
    if (!isSecretItem(item)) return refuse(SECURE_STORE_CODES.UNKNOWN_ITEM, 'item');
    if (typeof fn !== 'function') return refuse(SECURE_STORE_CODES.NOT_A_CALLBACK, 'fn');
    const read = await this.get(accountId, item);
    // A missing or unreadable item is this method's refusal too, verbatim --
    // `absent` is how a caller learns the account was never paired, which is a
    // decision for the caller and not something to paper over with an empty key.
    if (!read.ok) return read;
    const material = read.secret.reveal();
    let result;
    try {
      result = await fn(material);
    } catch {
      return refuse(SECURE_STORE_CODES.CALLBACK_FAILED, 'fn');
    }
    let leaked;
    try {
      leaked = carries(result, material);
    } catch {
      // A result this check cannot walk -- a Proxy that throws, a getter that
      // throws, a structure too deep to be sure about -- is refused: "could not
      // prove it does not carry the key" is not "does not carry the key".
      leaked = true;
    }
    if (leaked) return refuse(SECURE_STORE_CODES.LEAKED, 'fn');
    return Object.freeze({ ok: true, result });
  }

  /**
   * Which of the two items this account has. Probes every name `SECRET_ITEMS`
   * declares rather than enumerating (the platform cannot), and reports
   * presence only: the values are read to answer the question and are not
   * returned.
   *
   * The list is the one in `secret.js`, not a copy of it here. A third item
   * added to D111 is then probed, forgotten and fenced by every module at
   * once, instead of being silently missing from this loop -- which would look
   * exactly like an item the owner never paired.
   */
  async items(accountId) {
    if (!isAccountId(accountId)) return refuse(SECURE_STORE_CODES.BAD_ACCOUNT, 'accountId');
    const present = [];
    for (const item of SECRET_ITEMS) {
      const read = await this.get(accountId, item);
      if (read.ok) present.push(item);
    }
    return Object.freeze({ ok: true, items: Object.freeze(present) });
  }

  /**
   * Forget everything this account holds: every item `SECRET_ITEMS` declares,
   * whether or not it is there. Called when an account is removed from the app
   * (D114's switcher), and the only way one account's credential is deleted --
   * an account's items are reached by its own id or by nothing.
   *
   * **The delete is NEVER gated on a successful read, and that is the security
   * property of this method.** An earlier version skipped any item whose read
   * failed, which sounds cautious and is the opposite: a read of a Keychain
   * item fails exactly when the device is locked -- the state the port's own
   * `WHEN_UNLOCKED_THIS_DEVICE_ONLY` accessibility creates -- and a read can
   * also fail to re-validate a value this module did not write. Skipping then
   * let `AccountStore.remove` delete the tuple and return `ok`, leaving the ID
   * token and the per-pairing MAC key in the Keychain with **no code path that
   * can reach them**: `get`, `items` and `setSecret` all require an account
   * that no longer exists. A credential with no owner is the one outcome this
   * module must never produce, so:
   *
   *   * deletion is attempted unconditionally. A Keychain delete needs no read
   *     and no unlock, and it is idempotent -- deleting an item that is not
   *     there is a no-op, which is why this is safe on every device state;
   *   * a delete that FAILS is a `store-failed` refusal, and `AccountStore.remove`
   *     keeps the tuple when it sees one, so the account stays reachable and the
   *     removal can be run again once the phone is unlocked.
   *
   * The read that still happens is for the REPORT ONLY -- it is how `removed`
   * can say which items were there -- and it never decides whether to delete.
   * On a locked phone `removed` may therefore under-report what was deleted,
   * which is the safe direction: an under-reported deletion is invisible, an
   * orphaned credential is not.
   */
  async forget(accountId) {
    if (!isAccountId(accountId)) return refuse(SECURE_STORE_CODES.BAD_ACCOUNT, 'accountId');
    const removed = [];
    for (const item of SECRET_ITEMS) {
      const read = await this.get(accountId, item);
      try {
        await this.#port.deleteItem(secretKey(accountId, item));
      } catch {
        return refuse(SECURE_STORE_CODES.STORE_FAILED, 'item');
      }
      // `unreadable` counts as present: something IS stored under this key,
      // and it is now gone. Only an `absent` answer means there was nothing.
      if (read.ok || read.code === SECURE_STORE_CODES.UNREADABLE) removed.push(item);
    }
    return Object.freeze({ ok: true, removed: Object.freeze(removed) });
  }
}

/** The longest value `put` will hand the platform, re-exported for callers. */
export { SECRET_VALUE_MAX };

/** How deep `use` walks a callback's result looking for the material. */
const LEAK_DEPTH = 4;

/**
 * The shortest material the fence searches for inside a longer string.
 *
 * A substring test cannot tell "the callback handed the key back" from "the
 * callback handed back a value that happens to contain the key's characters",
 * and for short material the second is what it mostly finds: the value P3.6's
 * client returns from `use` is the 64-hex tag **it made with that key**, and a
 * two-character key is inside such a tag more often than not (the tag of a
 * fixed record with the key `ab` contains `ab`). Reading that coincidence as a
 * leak refuses every send the phone will ever make, over a value that tells the
 * caller nothing about the key.
 *
 * So: at or above this length an occurrence anywhere is evidence of a leak;
 * below it, only an occurrence that stands on its own is (see `delimited`),
 * because a coincidence is always *inside* the longer run of characters the
 * value is made of, and material handed back deliberately is not.
 *
 * The floor is the fence's own and not D111's: `secret()` sets no minimum on a
 * value's length (empty, unprintable and over the platform's ceiling are what
 * it refuses), and this module does not invent a rule about the credentials
 * `fugaro pair` mints. Both of D111's items -- a JWT and a 32-byte per-pairing
 * key -- are far above the line; the rule exists so that a shorter one is still
 * usable rather than refused as a leak.
 */
const LEAK_SEARCH_MIN = 16;

/** What counts as part of a longer word: the characters secrets and tags share. */
const WORD_CHAR_RE = /[A-Za-z0-9]/;

// delimited reports whether `material` occurs in `text` as a run of its own: at
// the start or preceded by something that is not a word character, and at the
// end or followed by one that is not. `key: ab` and `ab` are; the `ab` inside
// `...f8abfeba...` is not.
function delimited(text, material) {
  let from = 0;
  for (;;) {
    const at = text.indexOf(material, from);
    if (at === -1) return false;
    const before = at === 0 ? '' : text[at - 1];
    const after = text[at + material.length] ?? '';
    if (!WORD_CHAR_RE.test(before) && !WORD_CHAR_RE.test(after)) return true;
    from = at + 1;
  }
}

// leakIn is the string half of the fence: is `material` in `text` because the
// material came out, rather than because its characters coincide?
function leakIn(text, material) {
  if (!text.includes(material)) return false;
  return material.length >= LEAK_SEARCH_MIN || delimited(text, material);
}

// carries reports whether the material appears anywhere in a value a callback
// returned: in a string, in an array's items, in a plain object's own
// enumerable keys and values, four levels deep. It is the fence that keeps
// `use` from being a way to hand the key back up the stack by accident (see
// `use`), and its limit is named there: a Map, a Set, a class instance and
// anything deeper are not walked.
function carries(value, material, depth = 0) {
  if (typeof value === 'string') return leakIn(value, material);
  if (value === null || typeof value !== 'object') return false;
  // The depth bound is also what stops a self-referencing result (`a.self =
  // a`) from recursing for ever.
  if (depth >= LEAK_DEPTH) return false;
  if (Array.isArray(value)) return value.some((item) => carries(item, material, depth + 1));
  const proto = Object.getPrototypeOf(value);
  if (proto !== Object.prototype && proto !== null) return false;
  for (const key of Object.keys(value)) {
    if (leakIn(key, material)) return true;
    if (carries(value[key], material, depth + 1)) return true;
  }
  return false;
}
