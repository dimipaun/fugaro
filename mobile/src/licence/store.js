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
// changes nothing for the user. The cap of two is the store's, and it drops by
// the same rule rather than by arrival: when a third key arrives the store keeps
// the two that expire latest. Arrival order would be wrong twice over -- a key
// the verifier cannot read is worth nothing, and a key already held and pasted
// again is the newest arrival without being any newer a key, so a cap that
// counted arrivals could evict the renewal the owner is relying on and leave the
// key it superseded in its place.

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
 * The tail of the queue every `install` runs on, and it is the module's because
 * the entry is: `install` reads `fugaro.licence.keys` and then writes it, so two
 * of them interleaved could each write a set that misses the other's key -- and
 * the cap of two would then drop the older of the two rather than keep both. The
 * queue belongs to the entry and not to the store object, so two objects over
 * one store -- the app's and a second screen's, or two a test builds -- queue
 * with each other rather than each queueing their own.
 *
 * A read is not queued with them, and it is not because a read writes nothing:
 * `#read` clears an entry that does not decode. It is because that clear cannot
 * cost a key. An install beside it reads the same damaged entry -- only this
 * module writes it -- so the two clear one entry between them, and an install
 * that lands after the clear reads the no-key state and writes its key onto it.
 */
let installs = Promise.resolve();

/** serial runs one install after the one before it, and answers what it
 * answered. A failure is still the caller's to see: the tail the queue keeps
 * swallows it, because the next install must run whether the previous one threw
 * or not. */
function serial(work) {
  const done = installs.then(work, work);
  installs = done.then(
    () => undefined,
    () => undefined,
  );
  return done;
}

/**
 * The app's licence keys, over the platform store it is handed.
 *
 * Constructed over its two ports rather than creating them -- `new
 * LicenceStore({ port, verifier })` -- so a test can choose both, and the app
 * chooses only the port, through the door's `createLicenceStore`, which binds
 * this build's verifier: a store that made its own storage could not be tested,
 * and one a caller could hand any verifier to could trust a keyring this build
 * does not ship.
 */
export class LicenceStore {
  #port;
  #verifier;

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
   * install keeps one pasted key, if it verifies, and answers the verifier's
   * reading of the key that was pasted -- its claims and its state. That state is
   * the pasted key's own, which is not always the one `active` reports: an older
   * key pasted beside a newer one verifies and its claims are shown, while T26
   * still makes the newer one the key that applies.
   *
   * The text is stored as it is read: trimmed, because surrounding whitespace
   * is what a paste brings and the verifier reads the trimmed text, so the store
   * writes the key the refusal and the claims are about rather than a second
   * spelling of it. Pasting a key already held does not add it again, and a
   * third key is not kept by dropping the oldest arrival: see `#capped`.
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
    return serial(async () => {
      const held = await this.#read();
      if (!held.ok) return held;
      const kept = this.#capped(held.keys, text, now);
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

  /** #capped is the held key texts with the pasted one added, cut back to what
   * the store may hold -- and the cut keeps the two that expire latest.
   *
   * Not the newest arrival and not the oldest: the cap is about how much term
   * the app can hold, and the key that expires soonest is the one worth least.
   * A key the verifier cannot read is worth nothing at all and goes before any
   * key it can; among equals the earlier arrival goes first, so the set does not
   * depend on the order the cap was reached in. The survivors keep their arrival
   * order, which is the order `keys` answers in. */
  #capped(held, text, now) {
    const texts = [...held.filter((one) => one !== text), text];
    if (texts.length <= LICENCE_KEYS_MAX) return texts;
    const ranked = texts.map((one, arrival) => {
      const answer = this.#verifier.verify(one, now);
      return { one, arrival, expires: answer.ok ? answer.claims.expires : -Infinity };
    });
    ranked.sort((a, b) => (a.expires === b.expires ? b.arrival - a.arrival : Math.sign(b.expires - a.expires)));
    return ranked
      .slice(0, LICENCE_KEYS_MAX)
      .sort((a, b) => a.arrival - b.arrival)
      .map((kept) => kept.one);
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
