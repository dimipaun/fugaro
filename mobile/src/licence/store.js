// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The app's keeper of the licence key (subscriptions plan S2.8; the design is
// docs/licensing/licence-key.md, whose "Storage and hand-off" section leaves
// the app's own place to this task).
//
// **Where the app keeps the key, which is this task's decision.** Each product
// keeps the key where its own platform keeps a credential, and the app's
// platform has exactly two places, so the choice is between them:
//
//   - **not the Keychain.** `mobile/src/accounts/secure-store.js` holds what
//     `secret.js`'s `SECRET_ITEMS` allows and nothing else -- an `idToken` and
//     a `macKey`, a list D111 fixed at two items and D232's ruling of
//     2026-10-29 kept at two -- and the key's row in the design note says so
//     in as many words. Widening that list would take an owner ruling, and
//     nothing here needs one: what the key is, is why.
//   - **the ordinary store, under a namespace of this module's own.** The key
//     binds to an **organisation** rather than to a person or a machine: every
//     installation of that organisation holds the same one, nothing compares
//     it with the installation, and seats are compared and shown but never
//     enforced (D439). So the app's copy is a licence rather than an account
//     credential -- D111's "never in a settings file" is a rule about the two
//     credentials -- and it goes to the store the account tuple is kept in,
//     under `fugaro.licence.`, which no other module names and which the
//     account store's own fences cannot reach (`settings.js` owns
//     `fugaro.settings.` and reserves `fugaro.account.`).
//
// **What is readable there is readable, and that is accepted rather than
// overlooked.** The ordinary store is unencrypted: on Android a file under the
// app's data directory, on iOS a plist in the app container. Anyone who can
// read the container can read the key -- and can read no more with it than the
// key states, because there is no revocation to dodge (D308) and no phone-home
// to answer. The alternative that would hide it is the Keychain, which is
// closed to it.
//
// **The port is three methods, and it is the app's store, not this module's.**
// `getItem`, `setItem` and `deleteItem` are what `expo-async-storage.js` (in
// `src/accounts/`) already wraps, so the one file that knows what a device is
// can hand this store and the account store the same port; the
// namespace above is what keeps the two apart. Enumeration is deliberately
// absent: this store writes one entry, names it itself, and never needs to list
// what else the store holds.
//
// **The key is verified before it is written, and the verifier is a port.** A
// bad key never enters the store at all: `install` hands the pasted text to the
// app's verifier and stores nothing unless it verifies, so a refusal is the
// verifier's own code and this module has no second opinion about what a key is
// (the shape a caller sees is `verify.js`'s, and `LICENCE_CODES`' words are the
// answer). The verifier arrives as an argument for the reason `verify.js` takes
// its Ed25519 primitive as one: **this file imports nothing at all**, so the
// app's suite still runs in a fresh clone with no `node_modules`, and a store
// could not quietly build itself a verifier that trusts a keyring this build
// does not hold. `index.js` is the door that hands the app's own verifier in.
//
// **Why two keys, and which one applies.** The renewal arrives 30 days before
// the key it follows expires, so the store holds both and the one with the
// later `expires` is the one that applies (T26) -- decided by `verify.js`'s
// `latest`, never by the order they were pasted, so a renewal landing early
// changes nothing for the user. The cap of two is the store's: a third key
// replaces the oldest arrival, because at that point the oldest is the one the
// renewal superseded.

/** The refusal codes this store answers with. They are this module's and not
 * `LICENCE_CODES`': a refusal that came from the verifier carries one of those
 * words, and the only refusal the store makes itself is that its own store
 * could not be read or written. */
export const LICENCE_STORE_CODES = Object.freeze({
  STORE_FAILED: 'store-failed',
});

/** The namespace this store owns in the app's ordinary store. */
export const LICENCE_KEY_PREFIX = 'fugaro.licence.';

/** The one entry the store writes: the installed key texts, as a JSON array.
 * One entry rather than one per key, because what may be installed is a set of
 * at most two and a set written in one step cannot be read half-updated. */
export const LICENCE_KEYS_ENTRY = `${LICENCE_KEY_PREFIX}keys`;

/** How many keys the app keeps: two, because the renewal's 30-day overlap
 * installs the key that follows before its predecessor has run out (T26). */
export const LICENCE_KEYS_MAX = 2;

/** A frozen refusal, in the shape `verify.js`'s refusals use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * The app's licence keys, over the platform store it is handed.
 *
 * Constructed over its two ports rather than creating them -- `new
 * LicenceStore({ port, verifier })` -- so the app and a test choose both: a
 * store that made its own storage could not be tested, and one that built its
 * own verifier could trust a keyring this build does not ship.
 */
export class LicenceStore {
  #port;
  #verifier;
  /**
   * The tail of the queue `install` runs on. An install reads the entry and then
   * writes it, so two of them interleaved could each write a set that misses the
   * other's key -- and the cap of two would then drop the older of the two rather
   * than keep both. The reading half is not queued and needs no queue: `keys` and
   * `active` change nothing.
   */
  #queue = Promise.resolve();

  constructor({ port, verifier } = {}) {
    if (port === null || typeof port !== 'object') throw new TypeError('a store port is required');
    for (const method of ['getItem', 'setItem', 'deleteItem']) {
      if (typeof port[method] !== 'function') throw new TypeError(`the store port must provide ${method}`);
    }
    if (verifier === null || typeof verifier !== 'object') throw new TypeError("the app's licence verifier is required");
    for (const method of ['verify', 'latest']) {
      if (typeof verifier[method] !== 'function') throw new TypeError(`the verifier must provide ${method}`);
    }
    this.#port = port;
    this.#verifier = verifier;
  }

  /**
   * install keeps one pasted key, if it verifies, and answers the claims and the
   * state it puts the installation in.
   *
   * The text is stored as it is read: trimmed, because surrounding whitespace
   * is what a paste brings and the verifier reads the trimmed text, so the store
   * writes the key the refusal and the claims are about rather than a second
   * spelling of it.
   *
   * @param {unknown} raw the key text, as pasted
   * @param {Date} now the moment to read it at
   * @returns {Promise<object>} `verify.js`'s answer, or its refusal, or this
   *   store's own refusal when it could not be read or written
   */
  async install(raw, now) {
    const read = this.#verifier.verify(raw, now);
    if (!read.ok) return read;
    const text = raw.trim();
    return this.#queued(async () => {
      const held = await this.#read();
      if (!held.ok) return held;
      const kept = [...held.keys.filter((one) => one !== text), text].slice(-LICENCE_KEYS_MAX);
      const wrote = await this.#write(kept);
      if (wrote !== null) return wrote;
      return read;
    });
  }

  /**
   * keys answers the installed key texts, oldest arrival first, without
   * verifying them.
   *
   * What a screen shows from this is "a key this build cannot verify" as well as
   * a good one (the design's §5.4 state), so a text that no longer verifies --
   * a key id dropped from the keyring -- is reported rather than hidden. The
   * no-key state is the empty list and not a refusal (F43).
   *
   * @returns {Promise<object>} `{ ok: true, keys }` or a store refusal
   */
  async keys() {
    return this.#read();
  }

  /**
   * active answers the key that applies now and the state it is in: among the
   * installed keys that verify, the one with the later expiry (T26), which is
   * `verify.js`'s `latest`.
   *
   * @param {Date} now the moment to read the keys at
   * @returns {Promise<object|null>} `latest`'s answer, `null` when no installed
   *   key applies, or a store refusal when the store could not be read
   */
  async active(now) {
    const held = await this.#read();
    if (!held.ok) return held;
    return this.#verifier.latest(held.keys, now);
  }

  /** #queued runs one mutation after the last one, and answers what it answered.
   * The caller still sees a failure: the tail the queue keeps swallows it
   * because the next install must run whether the previous one threw or not. */
  #queued(work) {
    const done = this.#queue.then(work, work);
    this.#queue = done.then(
      () => undefined,
      () => undefined,
    );
    return done;
  }

  async #read() {
    let stored;
    try {
      stored = await this.#port.getItem(LICENCE_KEYS_ENTRY);
    } catch {
      return refuse(LICENCE_STORE_CODES.STORE_FAILED, 'keys');
    }
    if (stored === null || stored === undefined) return Object.freeze({ ok: true, keys: Object.freeze([]) });

    let parsed = null;
    try {
      parsed = JSON.parse(stored);
    } catch {
      parsed = null;
    }
    if (!Array.isArray(parsed) || !parsed.every((one) => typeof one === 'string' && one !== '')) {
      // An entry this store did not write: a version that spelled its keys
      // differently, or storage that came back damaged. Cleared rather than
      // read again, because an entry that fails to decode is not retried
      // forever -- and the state it leaves is the no-key state, which is a real
      // state the app has (S6.1) and one the owner can leave by pasting a key.
      return this.#clear();
    }
    return Object.freeze({ ok: true, keys: Object.freeze(parsed) });
  }

  /** #clear removes the entry and answers the no-key state, and refuses rather
   * than pretending it cleared when the delete itself failed -- otherwise every
   * later read would try again over the same blob. */
  async #clear() {
    try {
      await this.#port.deleteItem(LICENCE_KEYS_ENTRY);
    } catch {
      return refuse(LICENCE_STORE_CODES.STORE_FAILED, 'keys');
    }
    return Object.freeze({ ok: true, keys: Object.freeze([]) });
  }

  /** #write stores the key texts as the one entry, answering `null` on success
   * and the refusal to hand back otherwise. */
  async #write(keys) {
    try {
      await this.#port.setItem(LICENCE_KEYS_ENTRY, JSON.stringify(keys));
    } catch {
      return refuse(LICENCE_STORE_CODES.STORE_FAILED, 'keys');
    }
    return null;
  }
}
