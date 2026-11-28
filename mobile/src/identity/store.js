// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The identities the phone holds (task M1; D2790, D2793, D2785).
//
// **What an identity is, and what it is not.** D2790 puts an identity *above*
// the account: D114's account stays what it was -- a paired project, an
// installation -- and a signed-in identity is the thing a pairing is proved
// with. It is `(provider, sub, email, authTime)`: the provider's own stable id
// for the person, the address the provider vouched for, and when that sign-in
// happened. It is **not** a credential and holds no project: the provider
// session lives in platform secure storage (D2790 amends D232), and a project
// credential exists only after a pairing (D2783).
//
// **One item per identity, and one record beside it.** The durable half of a
// provider session -- the identity project's refresh token -- is written to
// the Keychain under `fugaro.identity-session.<identityId>` (the contract's
// item name, C1), while the fields a screen lists are a plain record. The two
// are written in a deliberate order: **the record first, the item second**, and
// a failed item write rolls the record back. An item with no record would be a
// credential with no owner -- the one outcome `src/accounts/store.js` also
// exists to refuse -- while a record whose item is missing fails closed the
// moment the identity is used.
//
// **The id is the contract's join, not a name invented here.** `identityId`
// (`shared/mobile-session`) joins the provider and the `sub` with a dot, and
// it is what a paired-list entry records (C1's `PairedEntry`): an identity the
// local store names one way and the identity project's list names another
// would be a restore that cannot find its own session.
//
// **Several identities are held independently** (F13, U4): each has its own
// record, its own item and its own cached ID token, and removing or
// re-authenticating one touches no other. A re-authentication that lands on a
// different `sub` is refused rather than absorbed: that is a different person,
// and replacing the session would show one person's access under another's
// name.
//
// **The relay flag is a fact about the address, not about the pairing.** An
// Apple private-relay address is not the address an admin invited (D2785), so
// `forPairing` refuses it here -- before a QR is scanned -- and an identity
// with no address at all is refused for the same reason.

import { identityId } from '../../../shared/mobile-session/index.js';
import { SECURE_STORE_CODES, isIdentitySessionId } from '../accounts/secure-store.js';
import { isProvider, isRelayAddress, refreshTokens, signInWithIdp } from './providers.js';

/** The settings key the identity records are stored under. */
export const IDENTITIES_KEY = 'fugaro.identities';

/** The document version the records are written with. */
const RECORDS_VERSION = 1;

/** The record fields, in the order they are written. This is the whole set. */
export const IDENTITY_FIELDS = Object.freeze(['id', 'provider', 'sub', 'email', 'authTime']);

/**
 * How long before its end a cached ID token is treated as spent. A token that
 * expires while a request is in flight is a request refused for a reason a
 * screen cannot fix, so the store renews early.
 */
const TOKEN_SKEW_MS = 60 * 1000;

/** The refusal codes this module returns. */
export const IDENTITY_CODES = Object.freeze({
  /** A `sub` no secure-store key could name. */
  BAD_IDENTITY: 'bad-identity',
  /** An identity the store does not hold. */
  UNKNOWN_IDENTITY: 'unknown-identity',
  /** The same provider and `sub` are already held. */
  DUPLICATE_IDENTITY: 'duplicate-identity',
  /** A re-authentication landed on another person (a different `sub`). */
  IDENTITY_CHANGED: 'identity-changed',
  /** The address is Apple's private relay, which no invite names (D2785). */
  RELAY_ADDRESS: 'relay-address',
  /** The identity carries no address at all. */
  NO_EMAIL: 'no-email',
  /** The records document is not one this store wrote. */
  UNREADABLE_RECORDS: 'unreadable-records',
  /** The item is gone from the platform store. */
  SESSION_ABSENT: 'session-absent',
  /** A store operation failed; what the platform said is not carried. */
  STORE_FAILED: 'store-failed',
});

/** A frozen refusal, in the shape the tree's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/** The entry a screen reads: the record and the two derived facts. */
function entry(record) {
  const relay = record.email !== null && isRelayAddress(record.email);
  return Object.freeze({ ...record, relay, usableForPairing: record.email !== null && !relay });
}

/**
 * Judge one stored record.
 *
 * Strict in both directions, as this tree's decoders are: an object with an
 * unknown key, a missing one, a value of the wrong type, or an `id` that is
 * not the join of its own provider and `sub`, is not a record this store
 * wrote. It is refused rather than repaired, because a record read loosely is
 * a record a screen lists under a name the store never wrote.
 */
function readRecord(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return null;
  const proto = Object.getPrototypeOf(value);
  if (proto !== Object.prototype && proto !== null) return null;
  const keys = Object.keys(value);
  if (keys.length !== IDENTITY_FIELDS.length) return null;
  for (const key of keys) {
    if (!IDENTITY_FIELDS.includes(key)) return null;
  }
  const { id, provider, sub, email, authTime } = value;
  if (typeof id !== 'string' || typeof provider !== 'string' || typeof sub !== 'string') return null;
  if (!isProvider(provider) || sub === '') return null;
  if (email !== null && (typeof email !== 'string' || email === '')) return null;
  if (!Number.isInteger(authTime) || authTime <= 0) return null;
  if (id !== identityId(provider, sub)) return null;
  return Object.freeze({ id, provider, sub, email, authTime });
}

/**
 * The identities the phone holds.
 *
 * Constructed over the ports rather than making them: `new IdentityStore({
 * settings, sessions, apiKey, signIn, refresh })`, where `settings` is a raw
 * key/value port (`getItem`/`setItem`/`deleteItem`, the shape
 * `expo-async-storage.js`'s `asyncStoragePort` and its in-memory double
 * `MemorySettingsStore` have) and `sessions` is an `IdentitySessionStore`
 * (`put`/`get`/`delete`) over the secure-store port. Both ports' methods are
 * required at construction, as `src/licence/store.js` requires its port's: a
 * wiring mistake is a `TypeError` there rather than a refusal on every call.
 * The two exchanges default to `providers.js` and are injectable so that a
 * test (and the plan's own flows) need no network.
 */
export class IdentityStore {
  #settings;
  #sessions;
  #apiKey;
  #signIn;
  #refresh;
  #now;
  /** id -> { idToken, expiresAt }, coherent with the stored session. */
  #tokens = new Map();
  /**
   * The tail of the queue every mutating call runs on, as the account store's
   * is: each of them reads a store, decides, and writes it back, so two
   * interleaved calls could each pass the other's check -- two records for one
   * identity, or a removal that read the document before an add wrote it.
   */
  #queue = Promise.resolve();

  constructor({ settings, sessions, apiKey, signIn, refresh, now }) {
    if (settings === null || typeof settings !== 'object') throw new TypeError('a settings port is required');
    // The three methods are required here, as `src/licence/store.js` requires
    // its port's: the app's other settings object (`SettingsStore`, which
    // validates values and exposes `get`/`set`) is an object this constructor
    // would otherwise accept and then answer `store-failed` on every call --
    // a failure nobody sees until a screen asks.
    for (const method of ['getItem', 'setItem', 'deleteItem']) {
      if (typeof settings[method] !== 'function') throw new TypeError(`the settings port must provide ${method}`);
    }
    if (sessions === null || typeof sessions !== 'object') throw new TypeError('an identity session store is required');
    // The same refusal for the sessions port, and for the same reason: the
    // app's secure-store PORT is an object with `getItem`/`setItem`/
    // `deleteItem`, one wrap away from the `IdentitySessionStore` this takes.
    // Handed in unwrapped it type-checks and then throws out of `add` -- after
    // the record was written, so the caller gets a rejection instead of a
    // refusal and a record is left with no item behind it.
    for (const method of ['put', 'get', 'delete']) {
      if (typeof sessions[method] !== 'function') throw new TypeError(`the identity session store must provide ${method}`);
    }
    if (typeof apiKey !== 'string' || apiKey === '') throw new TypeError('the identity project\'s API key is required');
    this.#settings = settings;
    this.#sessions = sessions;
    this.#apiKey = apiKey;
    this.#signIn = signIn ?? signInWithIdp;
    this.#refresh = refresh ?? refreshTokens;
    this.#now = now ?? Date.now;
  }

  /** Run `fn` after every mutation queued before it. A failure does not stall the queue. */
  #serial(fn) {
    const run = this.#queue.then(fn, fn);
    this.#queue = run.then(
      () => undefined,
      () => undefined,
    );
    return run;
  }

  /** The stored records, or the refusal of a document this store did not write. */
  async #read() {
    let text;
    try {
      text = await this.#settings.getItem(IDENTITIES_KEY);
    } catch {
      return refuse(IDENTITY_CODES.STORE_FAILED, 'records');
    }
    if (text === null || text === undefined) return { ok: true, records: [] };
    let document;
    try {
      document = JSON.parse(text);
    } catch {
      // A document that cannot be read is NOT cleared: it is the store of
      // record, and wiping it would silently unpair nobody knows what. The
      // refusal is loud and the next write replaces it only once a caller has
      // decided what to do.
      return refuse(IDENTITY_CODES.UNREADABLE_RECORDS, 'records');
    }
    if (document === null || typeof document !== 'object' || document.version !== RECORDS_VERSION) {
      return refuse(IDENTITY_CODES.UNREADABLE_RECORDS, 'records');
    }
    if (!Array.isArray(document.records)) return refuse(IDENTITY_CODES.UNREADABLE_RECORDS, 'records');
    const records = [];
    for (const value of document.records) {
      const record = readRecord(value);
      if (record === null) return refuse(IDENTITY_CODES.UNREADABLE_RECORDS, 'records');
      records.push(record);
    }
    return { ok: true, records };
  }

  /** Write the records back, all of them, or the refusal of a failed write. */
  async #write(records) {
    try {
      await this.#settings.setItem(IDENTITIES_KEY, JSON.stringify({ version: RECORDS_VERSION, records }));
    } catch {
      return refuse(IDENTITY_CODES.STORE_FAILED, 'records');
    }
    return { ok: true };
  }

  /** Forget one identity's cached token. Called wherever its session changes. */
  #forget(id) {
    this.#tokens.delete(id);
  }

  /** The identity's session item, as the token -- or the refusal. */
  async #tokenOf(id) {
    const read = await this.#sessions.get(id);
    if (read.ok) return read;
    if (read.code === SECURE_STORE_CODES.ABSENT) return refuse(IDENTITY_CODES.SESSION_ABSENT, 'session');
    return read;
  }

  /**
   * Every identity the phone holds, each with the relay flag and whether a
   * pairing may use it. The list carries no token and no item value.
   */
  async list() {
    const read = await this.#read();
    if (!read.ok) return read;
    return Object.freeze({ ok: true, identities: Object.freeze(read.records.map(entry)) });
  }

  /**
   * The identity a pairing may use, or the refusal that says why not: the
   * identity is unknown, carries no address, or carries Apple's private-relay
   * address -- which no invite names (D2785).
   */
  async forPairing(id) {
    const read = await this.#read();
    if (!read.ok) return read;
    const record = read.records.find((candidate) => candidate.id === id);
    if (record === undefined) return refuse(IDENTITY_CODES.UNKNOWN_IDENTITY, 'id');
    const listed = entry(record);
    if (listed.relay) return refuse(IDENTITY_CODES.RELAY_ADDRESS, 'email');
    if (record.email === null) return refuse(IDENTITY_CODES.NO_EMAIL, 'email');
    return Object.freeze({ ok: true, identity: listed });
  }

  /**
   * Sign one provider token in and hold it as an identity. The sign-in's own
   * refusal is answered unchanged, so a refusal from the identity project
   * reaches the caller as itself and nothing is written.
   */
  async add({ provider, idToken, nonce }) {
    return this.#serial(async () => {
      const signed = await this.#signIn({ apiKey: this.#apiKey, provider, idToken, nonce });
      if (!signed.ok) return signed;
      const { session } = signed;
      const id = identityId(session.provider, session.sub);
      // `identityId` refuses a part the keychain cannot hold; the predicate
      // adds the length bound, so an id no item could be named for is refused
      // here rather than by the store's write.
      if (id === null || !isIdentitySessionId(id)) return refuse(IDENTITY_CODES.BAD_IDENTITY, 'sub');
      const read = await this.#read();
      if (!read.ok) return read;
      const records = read.records;
      if (records.some((record) => record.id === id)) return refuse(IDENTITY_CODES.DUPLICATE_IDENTITY, 'id');
      const record = Object.freeze({
        id,
        provider: session.provider,
        sub: session.sub,
        email: session.email,
        authTime: session.authTime,
      });
      const written = await this.#write([...records, record]);
      if (!written.ok) return written;
      const stored = await this.#sessions.put(id, session.refreshToken);
      if (!stored.ok) {
        // The record goes back: an identity without its item lists a session
        // the phone does not have. A rollback that fails too leaves the record
        // behind -- its item is missing, so it fails closed, and `remove`
        // clears it -- and the refusal that comes back names the records rather
        // than saying a write that did not happen had.
        const rolledBack = await this.#write(records);
        return rolledBack.ok ? refuse(IDENTITY_CODES.STORE_FAILED, 'session') : rolledBack;
      }
      this.#cacheToken(id, session);
      return Object.freeze({ ok: true, identity: entry(record) });
    });
  }

  /**
   * Remove one identity: its item first and then its record, which is the
   * order that cannot leave a credential behind. A delete that fails keeps the
   * record, so the removal can be run again once the phone is unlocked.
   */
  async remove(id) {
    return this.#serial(async () => {
      const read = await this.#read();
      if (!read.ok) return read;
      const records = read.records;
      const record = records.find((candidate) => candidate.id === id);
      if (record === undefined) return refuse(IDENTITY_CODES.UNKNOWN_IDENTITY, 'id');
      const deleted = await this.#sessions.delete(id);
      if (!deleted.ok) return refuse(IDENTITY_CODES.STORE_FAILED, 'session');
      // The item is gone, so the cached token goes with it, whether or not the
      // record write below lands.
      this.#forget(id);
      const written = await this.#write(records.filter((candidate) => candidate.id !== id));
      if (!written.ok) return written;
      return Object.freeze({ ok: true, removed: entry(record) });
    });
  }

  /**
   * Sign the same provider in again, which is what a project means by
   * `auth_too_old`: the address and the authentication time are refreshed and
   * the session is replaced. A sign-in that comes back with another `sub` is
   * refused -- it is a different person, and absorbing it would show one
   * person's access under another's name.
   */
  async reauthenticate(id, { idToken, nonce }) {
    return this.#serial(async () => {
      const read = await this.#read();
      if (!read.ok) return read;
      const records = read.records;
      const record = records.find((candidate) => candidate.id === id);
      if (record === undefined) return refuse(IDENTITY_CODES.UNKNOWN_IDENTITY, 'id');
      const signed = await this.#signIn({ apiKey: this.#apiKey, provider: record.provider, idToken, nonce });
      if (!signed.ok) return signed;
      if (signed.session.sub !== record.sub) return refuse(IDENTITY_CODES.IDENTITY_CHANGED, 'sub');
      const stored = await this.#sessions.put(id, signed.session.refreshToken);
      if (!stored.ok) return stored;
      const updated = Object.freeze({ ...record, email: signed.session.email, authTime: signed.session.authTime });
      const written = await this.#write(records.map((candidate) => (candidate.id === id ? updated : candidate)));
      if (!written.ok) return written;
      this.#cacheToken(id, signed.session);
      return Object.freeze({ ok: true, identity: entry(updated) });
    });
  }

  /**
   * A live ID token for one identity: the cached one while it has time left,
   * otherwise a renewal through the stored session. The renewal rotates the
   * refresh token, so the successor is written back before the token is
   * answered, and a write that failed is a refusal rather than a cached token
   * (a value the store did not take is not remembered).
   */
  async idToken(id) {
    return this.#serial(async () => {
      const read = await this.#read();
      if (!read.ok) return read;
      const record = read.records.find((candidate) => candidate.id === id);
      if (record === undefined) return refuse(IDENTITY_CODES.UNKNOWN_IDENTITY, 'id');
      const cached = this.#tokens.get(id);
      if (cached !== undefined && cached.expiresAt - TOKEN_SKEW_MS > this.#now()) {
        return Object.freeze({ ok: true, idToken: cached.idToken });
      }
      const issued = await this.#tokenOf(id);
      if (!issued.ok) return issued;
      const renewed = await this.#refresh({ apiKey: this.#apiKey, refreshToken: issued.token });
      if (!renewed.ok) return renewed;
      const stored = await this.#sessions.put(id, renewed.tokens.refreshToken);
      if (!stored.ok) return refuse(IDENTITY_CODES.STORE_FAILED, 'session');
      this.#cacheToken(id, { idToken: renewed.tokens.idToken, expiresIn: renewed.tokens.expiresIn });
      return Object.freeze({ ok: true, idToken: renewed.tokens.idToken });
    });
  }

  /** Remember one ID token for its life, measured from now. */
  #cacheToken(id, session) {
    this.#tokens.set(id, {
      idToken: session.idToken,
      expiresAt: this.#now() + session.expiresIn * 1000,
    });
  }
}
