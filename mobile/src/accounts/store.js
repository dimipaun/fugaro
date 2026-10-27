// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The account store (mobile plan, Task P3.1; decisions D114, D111, D125).
//
// D114 defines an account as an **installation** and says how several are
// held: as "separate, **non-overlapping** entries -- a per-account Keychain
// item, a per-account cache namespace, no shared token store -- and the
// switcher **tears down the previous account's listeners and caches before it
// opens the next**". This module is that sentence, over the three modules
// beside it: `account.js` (the tuple and its rules), `secure-store.js` (the
// Keychain item, per account), `settings.js` and `resources.js` (the tuple on
// disk, and the listeners and cache of an open account).
//
// **What it holds.** A tuple's non-secret fields, in the settings store, under
// `account.<id>`; each account's two items in platform secure storage, under
// `fugaro.account.<id>.<item>`; and **one field of its own** -- which account
// is current. It never holds a secret in memory beyond the moment a caller
// asks for one, and `list()` returns no secret at all: the public view of an
// account is D114's tuple, and `secret(id, item)` is the only method that
// returns material, one item at a time, for an account that exists.
//
// **The order of a switch is the security property, not a detail.** A listener
// left alive after a switch is a subscription still authenticated as the
// previous installation, delivering that installation's data into a screen
// showing another account -- check 87's cross-leakage, in the least visible
// form. So `switchTo` **closes first and opens second**, and it refuses to
// open without a completed close: a teardown that reported a failure leaves
// the store with NO current account rather than with a new one, because "two
// installations live at once" is the state D114 exists to forbid. The test
// asserts the order on the registry's trace and the count of live listeners
// afterwards, so the sentence is a fact rather than a comment.
//
// **The order of a removal is the other half.** The secrets go first, then the
// listeners, then the tuple. If a credential is left in the Keychain with no
// account pointing at it, nothing will ever delete it -- it is a secret with
// no owner, which is worse than an account with no secret (that one fails
// closed the moment it is used). So `remove` forgets the items first and, if
// that fails, removes nothing at all: the account is intact and can be removed
// again.
//
// **The account list is typed by the owner.** D114: it is "a list the owner
// maintains, not a discovery result" -- the app has no method that finds an
// installation, `add` is the only way in, and a fresh store holds nothing. The
// `belong` installation is added by the owner by hand and is never touched by
// an agent, and no fixture in this directory is ever recorded from it (Global
// Constraint 11).

import { isAccountId, sameInstallation, validateAccount } from './account.js';
import { isSecretItem } from './secret.js';
import { RESOURCE_CODES } from './resources.js';
import { ACCOUNT_KEY_PREFIX } from './settings.js';

/**
 * The settings key prefix an account tuple is stored under, re-exported (it is
 * declared in `settings.js`, which owns settings keys, and its key fence has to
 * know the same string this module composes with). One value, one name, and the
 * module's door exports it from here so callers need not know about the split.
 */
export { ACCOUNT_KEY_PREFIX };

/** The refusal codes this module returns. */
export const STORE_CODES = Object.freeze({
  BAD_ACCOUNT: 'bad-account',
  UNKNOWN_ACCOUNT: 'unknown-account',
  DUPLICATE_ID: 'duplicate-id',
  DUPLICATE_INSTALLATION: 'duplicate-installation',
  UNREADABLE_ACCOUNT: 'unreadable-account',
  TEARDOWN_FAILED: RESOURCE_CODES.TEARDOWN_FAILED,
});

/** A frozen refusal, in the shape the shared package's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * The accounts the phone holds, and which one is open.
 *
 * Constructed over the three stores rather than creating them, so a test (and
 * the app) can choose the ports: `new AccountStore({ secrets, settings,
 * registry })`. All three are required -- a store that silently made its own
 * Keychain would be a store whose isolation rules could not be tested.
 */
export class AccountStore {
  #secrets;
  #settings;
  #registry;
  #current = null;
  /**
   * The tail of the queue every MUTATING method runs on. Each of them awaits a
   * store between its check and its act (`add` lists and then writes, `remove`
   * forgets and then deletes, `switchTo` reads and then opens), so two of them
   * interleaved could each pass the other's check: two entries for one
   * installation, a removed account opened by a switch that read its tuple a
   * moment earlier, or a secret written after `remove` forgot the account's
   * items -- a credential with no owner. Running them one at a time makes each
   * check and its act one step. Reads (`list`, `account`, `secret`, `items`)
   * do not queue: they change nothing.
   */
  #queue = Promise.resolve();

  constructor({ secrets, settings, registry }) {
    if (secrets === null || typeof secrets !== 'object') throw new TypeError('a secret store is required');
    if (settings === null || typeof settings !== 'object') throw new TypeError('a settings store is required');
    if (registry === null || typeof registry !== 'object') throw new TypeError('a resource registry is required');
    this.#secrets = secrets;
    this.#settings = settings;
    this.#registry = registry;
  }

  /** Run `fn` after every mutation queued before it, and before any queued
   * after it. A failure in one does not stall the queue. */
  #serial(fn) {
    const run = this.#queue.then(fn, fn);
    this.#queue = run.then(
      () => undefined,
      () => undefined,
    );
    return run;
  }

  /**
   * Close EVERY account the registry holds open, not only the one this store
   * recorded as current. The registry is shared (a later task's reader is
   * handed it), so an account opened on it behind this store's back would
   * otherwise survive a switch -- two installations live at once, the state
   * D114 forbids. Returns the first account whose teardown failed, if any,
   * and the total failure count; leaves no account open and none current.
   */
  #closeEverything() {
    let failedAccount = null;
    let failures = 0;
    for (const accountId of this.#registry.openAccounts()) {
      const closed = this.#registry.close(accountId);
      if (!closed.ok) {
        failedAccount ??= accountId;
        failures += closed.failures ?? 1;
      }
    }
    this.#current = null;
    return { failedAccount, failures };
  }

  static #keyOf(accountId) {
    return `${ACCOUNT_KEY_PREFIX}${accountId}`;
  }

  /** One stored tuple, validated. A stored tuple this module could not have
   * written is `unreadable-account`, never repaired into an installation. */
  async #read(accountId) {
    if (!isAccountId(accountId)) return refuse(STORE_CODES.BAD_ACCOUNT, 'accountId');
    const read = await this.#settings.get(AccountStore.#keyOf(accountId));
    if (!read.ok) {
      return read.code === 'absent' ? refuse(STORE_CODES.UNKNOWN_ACCOUNT, 'accountId') : read;
    }
    const valid = validateAccount(read.value);
    if (!valid.ok) return refuse(STORE_CODES.UNREADABLE_ACCOUNT, 'accountId');
    if (valid.account.id !== accountId) return refuse(STORE_CODES.UNREADABLE_ACCOUNT, 'accountId');
    return valid;
  }

  /**
   * Every account the phone holds, as D114's tuple and nothing else.
   *
   * The public view carries no secret: there is no field here a credential
   * could be added to without changing `ACCOUNT_FIELDS`, and `mobile/README.md`
   * states the two items and where they are instead. An account whose stored
   * tuple cannot be read fails the whole call, because a list that quietly
   * dropped one installation is a list the switcher would then be missing an
   * entry from.
   */
  async list() {
    const keys = await this.#settings.keys();
    if (!keys.ok) return keys;
    const accounts = [];
    for (const key of keys.keys) {
      if (!key.startsWith(ACCOUNT_KEY_PREFIX)) continue;
      const read = await this.#read(key.slice(ACCOUNT_KEY_PREFIX.length));
      if (!read.ok) return read;
      accounts.push(read.account);
    }
    accounts.sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
    return Object.freeze({ ok: true, accounts: Object.freeze(accounts) });
  }

  /** One account, by id, as D114's tuple. */
  async account(accountId) {
    return this.#read(accountId);
  }

  /**
   * Add an installation. The only way an account enters the app (D114: the
   * list is typed by the owner), and it refuses a second account that is the
   * same installation under another id -- two entries for one installation
   * would be two cache namespaces and two Keychain items for one project,
   * which is D114's "non-overlapping" broken in the quiet direction.
   */
  async add(value) {
    return this.#serial(() => this.#add(value));
  }

  async #add(value) {
    const valid = validateAccount(value);
    if (!valid.ok) return valid;
    const { account } = valid;
    const existing = await this.list();
    if (!existing.ok) return existing;
    for (const held of existing.accounts) {
      if (held.id === account.id) return refuse(STORE_CODES.DUPLICATE_ID, 'id');
      if (sameInstallation(held, account)) {
        return refuse(STORE_CODES.DUPLICATE_INSTALLATION, 'firebaseProjectId');
      }
    }
    const written = await this.#settings.set(AccountStore.#keyOf(account.id), account);
    if (!written.ok) return written;
    return Object.freeze({ ok: true, account });
  }

  /**
   * Remove an installation: its secrets first, then its listeners and cache,
   * then the tuple (see the header for why that order).
   */
  async remove(accountId) {
    return this.#serial(() => this.#remove(accountId));
  }

  async #remove(accountId) {
    const held = await this.#read(accountId);
    if (!held.ok) return held;
    const forgotten = await this.#secrets.forget(accountId);
    if (!forgotten.ok) return forgotten;
    let teardownFailures = 0;
    if (this.#registry.isOpen(accountId)) {
      const closed = this.#registry.close(accountId);
      if (!closed.ok) teardownFailures = closed.failures;
    }
    if (this.#current === accountId) this.#current = null;
    const deleted = await this.#settings.delete(AccountStore.#keyOf(accountId));
    if (!deleted.ok) return deleted;
    return Object.freeze({ ok: true, removed: forgotten.removed, teardownFailures });
  }

  /** Which account is open, or `null`. */
  current() {
    return this.#current;
  }

  /**
   * Open an account, closing the previous one first.
   *
   * Returns `{ ok: true, changed }`. A teardown that failed, or an open that
   * failed, leaves the store with **no** current account: the previous one has
   * been closed and the next one is not opened, so there is never a moment
   * with two installations live.
   */
  async switchTo(accountId) {
    return this.#serial(() => this.#switchTo(accountId));
  }

  async #switchTo(accountId) {
    const held = await this.#read(accountId);
    if (!held.ok) return held;
    const open = this.#registry.openAccounts();
    if (this.#current === accountId && open.length === 1 && open[0] === accountId) {
      return Object.freeze({ ok: true, changed: false, account: held.account });
    }
    const previous = this.#current;
    const { failedAccount, failures } = this.#closeEverything();
    if (failures > 0) {
      // Every open account is closed (its listeners are stopped and its cache
      // is empty) but something inside one failed. Opening the next one now
      // would be the two-live-accounts state; refuse instead.
      return Object.freeze({
        ok: false,
        code: STORE_CODES.TEARDOWN_FAILED,
        accountId: previous ?? failedAccount,
        failures,
      });
    }
    const opened = this.#registry.open(accountId);
    if (!opened.ok) return opened;
    this.#current = accountId;
    return Object.freeze({ ok: true, changed: true, account: held.account });
  }

  /** Close the current account, leaving none open. */
  async closeCurrent() {
    return this.#serial(() => this.#closeCurrent());
  }

  #closeCurrent() {
    if (this.#current === null && this.#registry.openAccounts().length === 0) {
      return Object.freeze({ ok: true, changed: false });
    }
    const previous = this.#current;
    const { failedAccount, failures } = this.#closeEverything();
    if (failures > 0) {
      return Object.freeze({ ok: false, code: STORE_CODES.TEARDOWN_FAILED, accountId: previous ?? failedAccount, failures });
    }
    return Object.freeze({ ok: true, changed: true });
  }

  /**
   * Write one of D111's two items for one account. The value is a `Secret`,
   * and the account must exist: a credential written for an account the app
   * does not list is a credential with no owner.
   */
  async setSecret(accountId, item, value) {
    return this.#serial(() => this.#setSecret(accountId, item, value));
  }

  async #setSecret(accountId, item, value) {
    const held = await this.#read(accountId);
    if (!held.ok) return held;
    if (!isSecretItem(item)) return refuse('unknown-item', 'item');
    return this.#secrets.put(accountId, item, value);
  }

  /** Read one of D111's two items for one account, wrapped: the only method
   * that returns material, and it needs the account and the item both. */
  async secret(accountId, item) {
    const held = await this.#read(accountId);
    if (!held.ok) return held;
    if (!isSecretItem(item)) return refuse('unknown-item', 'item');
    return this.#secrets.get(accountId, item);
  }

  /** Which of the two items this account holds. Names, never values. */
  async items(accountId) {
    const held = await this.#read(accountId);
    if (!held.ok) return held;
    return this.#secrets.items(accountId);
  }
}
