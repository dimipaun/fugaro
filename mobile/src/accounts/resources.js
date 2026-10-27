// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// What belongs to an open account, and how it is torn down (Task P3.1; D114).
//
// D114's account model is not only a tuple: several installations are stored
// as "separate, **non-overlapping** entries -- a per-account Keychain item, a
// per-account cache namespace, no shared token store -- and the switcher
// **tears down the previous account's listeners and caches before it opens the
// next**". `secure-store.js` is the Keychain item (one per account, composed
// from its id) and `settings.js` is the tuple. This module is the other two:
// the cache namespace, and the listeners that must not outlive the account
// that opened them.
//
// **Why the ordering is the whole point.** A listener left alive after a
// switch is a subscription still authenticated as the previous installation:
// it keeps a socket open, it keeps receiving that installation's data, and the
// value it delivers arrives in a process now showing another account -- which
// is D114's "no cross-leakage" (check 87) failing in the least visible way
// there is. So a switch is **close, then open**, and the store (`store.js`)
// never opens the next account until the previous one's teardown has run to
// the end, even if one listener's own `stop()` threw.
//
// **Not a supervisor, and not a client.** Nothing here opens a socket or
// subscribes to anything: a listener arrives as an opaque handle -- anything
// with a `stop()`, or a function, which is the shape the Realtime Database
// SDK's `onValue` already returns an unsubscribe in -- and this module only
// keeps it, counts it and stops it. P3.2's reader is the first caller; the
// fakes below are what lets its rules be tested with no database and no
// device (Global Constraint 10).

import { isAccountId } from './account.js';

/** The prefix a cache namespace carries. */
export const CACHE_PREFIX = 'fugaro.cache.';

/** The refusal codes this module returns. */
export const RESOURCE_CODES = Object.freeze({
  BAD_ACCOUNT: 'bad-account',
  NOT_OPEN: 'not-open',
  ALREADY_OPEN: 'already-open',
  BAD_LISTENER: 'bad-listener',
  TEARDOWN_FAILED: 'teardown-failed',
});

/** A frozen refusal, in the shape the shared package's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * The namespace one account's cache lives in. Throws on an id it cannot
 * compose, for the reason `secretKey` does: a namespace shared between two
 * accounts is a cache read answering with the other installation's data.
 */
export function cacheNamespace(accountId) {
  if (!isAccountId(accountId)) throw new TypeError('not an account id');
  return `${CACHE_PREFIX}${accountId}`;
}

/**
 * One account's cache: a `Map`, namespaced, and readable only while the
 * account is open (the registry decides that, and `cacheFor` refuses
 * otherwise).
 */
export class AccountCache {
  #namespace;
  #entries = new Map();

  constructor(namespace) {
    this.#namespace = namespace;
  }

  get namespace() {
    return this.#namespace;
  }

  get(key) {
    return this.#entries.get(key);
  }

  set(key, value) {
    if (typeof key !== 'string' || key.length === 0) throw new TypeError('a cache key is a non-empty string');
    this.#entries.set(key, value);
  }

  delete(key) {
    return this.#entries.delete(key);
  }

  /** Forget everything this account cached. Called by the registry's close. */
  clear() {
    this.#entries.clear();
  }

  size() {
    return this.#entries.size;
  }

  keys() {
    return [...this.#entries.keys()].sort();
  }
}

/**
 * The per-account listeners and caches, and the switch between accounts.
 *
 * One registry serves the whole app: an account is open or it is not, its
 * listeners live while it is, and closing it stops every one of them, empties
 * its cache and forgets its namespace.
 */
export class ResourceRegistry {
  #open = new Set();
  #listeners = new Map();
  #caches = new Map();
  #trace;

  /**
   * @param options.trace an array to append the registry's ordering to
   *   (`['open', id]`, `['listener', id]`, `['close', id]`). It is the seam
   *   that makes "close before open" a fact a test can read rather than an
   *   order a test has to infer from a side effect -- the same reason the
   *   shared modules take an injected clock.
   */
  constructor({ trace } = {}) {
    this.#trace = trace ?? null;
  }

  #note(event, accountId) {
    if (this.#trace !== null) this.#trace.push([event, accountId]);
  }

  isOpen(accountId) {
    return this.#open.has(accountId);
  }

  /** The accounts that are open right now, in order. */
  openAccounts() {
    return [...this.#open].sort();
  }

  /** Open an account. Opening one that is already open is refused rather than
   * treated as success, so a caller cannot skip a teardown by opening twice. */
  open(accountId) {
    if (!isAccountId(accountId)) return refuse(RESOURCE_CODES.BAD_ACCOUNT, 'accountId');
    if (this.#open.has(accountId)) return refuse(RESOURCE_CODES.ALREADY_OPEN, 'accountId');
    this.#open.add(accountId);
    this.#listeners.set(accountId, new Set());
    this.#caches.set(accountId, new AccountCache(cacheNamespace(accountId)));
    this.#note('open', accountId);
    return Object.freeze({ ok: true });
  }

  /**
   * Register a listener for an open account. `listener` is anything with a
   * `stop()` method, or a function -- the unsubscribe a subscription returns.
   * The handle is alive until the account is closed; a listener registered for
   * an account that is not open is refused, because nothing would ever stop
   * it.
   */
  add(accountId, listener) {
    if (!isAccountId(accountId)) return refuse(RESOURCE_CODES.BAD_ACCOUNT, 'accountId');
    if (!this.#open.has(accountId)) return refuse(RESOURCE_CODES.NOT_OPEN, 'accountId');
    const stop =
      typeof listener === 'function'
        ? listener
        : listener !== null && typeof listener === 'object' && typeof listener.stop === 'function'
          ? () => listener.stop()
          : null;
    if (stop === null) return refuse(RESOURCE_CODES.BAD_LISTENER, 'listener');
    this.#listeners.get(accountId).add(stop);
    this.#note('listener', accountId);
    return Object.freeze({ ok: true, count: this.#listeners.get(accountId).size });
  }

  /** How many listeners this account still has alive. */
  liveCount(accountId) {
    return this.#listeners.get(accountId)?.size ?? 0;
  }

  /** This account's cache, if the account is open. A closed account has none:
   * its cache was cleared at close, and a read after that must not find it
   * again. */
  cacheFor(accountId) {
    if (!isAccountId(accountId)) return refuse(RESOURCE_CODES.BAD_ACCOUNT, 'accountId');
    if (!this.#open.has(accountId)) return refuse(RESOURCE_CODES.NOT_OPEN, 'accountId');
    return Object.freeze({ ok: true, cache: this.#caches.get(accountId) });
  }

  /**
   * Close an account: stop every listener, empty its cache, forget its
   * namespace.
   *
   * **A listener whose `stop()` throws does not stop the teardown.** Every
   * other listener is still stopped and the cache is still emptied, because a
   * half-closed account is exactly the state this module exists to prevent;
   * the failures are reported afterwards, so the caller can see that something
   * went wrong without the account staying open. The result is a
   * `teardown-failed` refusal carrying the count, and the account is **not**
   * open either way -- fail closed, in the direction that has no listener
   * alive.
   */
  close(accountId) {
    if (!isAccountId(accountId)) return refuse(RESOURCE_CODES.BAD_ACCOUNT, 'accountId');
    if (!this.#open.has(accountId)) return refuse(RESOURCE_CODES.NOT_OPEN, 'accountId');
    const stops = [...(this.#listeners.get(accountId) ?? [])];
    let failures = 0;
    for (const stop of stops) {
      try {
        stop();
      } catch {
        failures++;
      }
    }
    this.#listeners.delete(accountId);
    this.#caches.get(accountId)?.clear();
    this.#caches.delete(accountId);
    this.#open.delete(accountId);
    this.#note('close', accountId);
    if (failures > 0) {
      return Object.freeze({ ok: false, code: RESOURCE_CODES.TEARDOWN_FAILED, failures });
    }
    return Object.freeze({ ok: true, stopped: stops.length });
  }

  /** Close every open account. Used when the app goes to the background or a
   * test is done; the same close, account by account. */
  closeAll() {
    const failed = [];
    for (const accountId of this.openAccounts()) {
      const closed = this.close(accountId);
      if (!closed.ok) failed.push(accountId);
    }
    if (failed.length > 0) {
      return Object.freeze({ ok: false, code: RESOURCE_CODES.TEARDOWN_FAILED, failures: failed.length });
    }
    return Object.freeze({ ok: true });
  }
}
