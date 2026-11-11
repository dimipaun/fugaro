// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The pairing session: §7.2's contract, in the order it is written (mobile
// plan, Task P6.8; the pairing plan's §7.2, §4.4, D111, D114, D165, D171,
// D199).
//
// **The order is the security property, so it is one function.** §7.2:
//
//     scan → validate before anything is stored → generate a key pair → show
//     the code derived from the public key → write {key, at} to the request
//     node once → sign in with the pairing token → watch `pairing/$uid` →
//     refuse the whole payload unless the envelope opens, the version is this
//     build's, and the AAD names this uid and this key → store the device token
//     and the MAC key in secure storage only → store the configuration from the
//     payload, not the scan (D199)
//
// Every step above is a line of `#start` or `#delivered` below, in that order,
// and each one refuses rather than continuing: a payload that is not a payload
// never reaches a key pair, a key pair whose request write failed never shows a
// code, and a delivery that does not open stores nothing at all. The whole
// session is one object because the alternative -- a caller sequencing these
// steps -- is a caller that can get the order wrong, and this is the one
// sequence in the app where getting it wrong leaves a credential in the wrong
// place.
//
// **Why the code is generated here and stored nowhere.** The code is the
// fingerprint of the phone's own public key (§7.2, M3), derived when the key
// pair is drawn and held in this object's memory for the length of the pairing.
// It is not written to the request node (the CLI derives it from the key it
// reads), not to the settings store, not to secure storage, and not to a
// fixture: the CLI's `pair.go` prints the code it derived and the phone prints
// the one it derived, and the pairing *is* the two agreeing. A stored code
// would be a third copy that could disagree -- and a code on disk is the one
// thing M3's "what a photographed QR does not give you" rests on.
//
// **Why the watch is a listener and not a read** (§7.2's amendment of
// 2026-10-30, T3b): the CLI writes the delivery and deletes the whole
// `pairing/$uid` subtree in the next instant, because the payload must not sit
// readable for the hour the `p~` ID token lives. A one-shot read after sign-in
// races a window microseconds wide and loses it, so the contract is a `value`
// observer attached as soon as the token is exchanged, holding the first
// snapshot that carries a delivery. A node that answers `null` before any
// delivery arrived is the pairing **expired**: the operator's wait ended
// without a completed code, or the sweep removed the session (decision 1), and
// §7.2's answer is to say so and show the code path again -- never to re-pair
// without an operator.
//
// **What is deliberately not here.** Persisting the delivered configuration.
// D199 makes the delivery's copy the record, and D259's amendment of
// 2026-10-29 says that configuration lives in secure storage -- but
// `accounts/secret.js`'s `SECRET_ITEMS` is exactly two (`idToken`, `macKey`)
// and `settings.js` refuses an `AIza…`-shaped value outright, so there is no
// item for it in this build. Widening D111's item list is P3.1's decision and
// not this task's file list, so the session holds the configuration for the
// pairing (its snapshot's `record`) and stores nothing else. That gap is
// reported rather than papered over: a third item written from here would be a
// storage contract invented in the wrong module.
//
// The account id is the one thing §7.2 does not name and cannot: D114's list is
// "a list the owner maintains, not a discovery result", so the label is the
// owner's, it is supplied to `start` (the pairing screen's text field), and
// this module only holds it to `account.js`'s own rule.

import { isAccountId, secret as makeSecret } from '../accounts/index.js';

import { encodeBase64url } from './base64url.js';
import { readDelivery } from './delivery.js';
import { decodeEnvelope, openDelivery } from './envelope.js';
import { encodeDeviceKey, fingerprint, generateKeyPair, isDriver } from './key.js';
import { readScannedPayload } from './payload.js';

/** The states a session is in, in the order it reaches them. */
export const SESSION_STATES = Object.freeze({
  IDLE: 'idle',
  WATCHING: 'watching',
  DELIVERED: 'delivered',
  REFUSED: 'refused',
  EXPIRED: 'expired',
});

/** How this module refuses, on top of the payload's and the delivery's codes:
 * a `scan` refusal is `payload.js`'s, a delivery refusal is `delivery.js`'s or
 * `envelope.js`'s, and a refusal of the session's own sequencing is here. */
export const SESSION_CODES = Object.freeze({
  BAD_PORT: 'bad-port',
  PORT_FAILED: 'port-failed',
  BAD_ACCOUNT: 'bad-account',
  ALREADY_STARTED: 'already-started',
  NOT_STARTED: 'not-started',
  REQUEST_REFUSED: 'request-refused',
  SIGN_IN_REFUSED: 'sign-in-refused',
  IDENTITY_MISMATCH: 'identity-mismatch',
  WATCH_REFUSED: 'watch-refused',
  STORE_REFUSED: 'store-refused',
  EXPIRED: 'expired',
});

/** What §7.2's request node holds: the phone's public key in its one pinned
 * spelling, and when it was written. */
export function requestPath(uid) {
  return `pairing/${uid}/request`;
}

/** What the phone watches: the whole pairing node, so the delivery and the
 * delete are both seen. */
export function pairingPath(uid) {
  return `pairing/${uid}`;
}

function refuse(code, field) {
  return Object.freeze({ ok: false, code, ...(field === undefined ? {} : { field }) });
}

/** An instant in the spelling `pair.go` writes: RFC3339 UTC. */
function instant(nowMs) {
  return new Date(nowMs).toISOString();
}

/**
 * One pairing, from a scanned string to two items in secure storage.
 *
 * The ports are the device's, injected so a test needs no device (Global
 * Constraint 10):
 *
 *   * `x25519` -- `getPublicKey` / `getSharedSecret`, the app's dependency;
 *   * `random` -- `randomBytes(n)`, the key pair's draw (a function, as
 *     `key.js`'s `generateKeyPair` takes it);
 *   * `clock` -- `nowMs()`, for the request's `at` and nothing else (the code's
 *     life is the CLI's `exp`, and the phone enforces no expiry of its own:
 *     `payload.js` reads the instant and does not judge it);
 *   * `database` -- `set(path, value)` and `watch(path, onValue, onError)`;
 *   * `auth` -- `signIn({ apiKey, databaseUrl, customToken })`, the Identity
 *     Toolkit exchange;
 *   * `store` -- the `AccountStore`, the only door to the tuple and the two
 *     items (D114, D111).
 */
export function createPairingSession({ x25519, random, clock, database, auth, store }) {
  if (!isDriver(x25519, 'getPublicKey')) throw new TypeError('an x25519 port is required');
  if (typeof random !== 'function') throw new TypeError('a random port is required');
  if (!isDriver(clock, 'nowMs')) throw new TypeError('a clock port is required');
  if (!isDriver(database, 'set') || !isDriver(database, 'watch')) throw new TypeError('a database port is required');
  if (!isDriver(auth, 'signIn')) throw new TypeError('an auth port is required');
  if (store === null || typeof store !== 'object') throw new TypeError('an account store is required');

  let state = SESSION_STATES.IDLE;
  let scan = null;
  let accountId = null;
  let secret = null;
  let publicKey = null;
  let code = null;
  let record = null;
  let refusal = null;
  let stop = null;
  let signIn = null;

  /** What the screen draws. No credential is ever in here: the token and the
   * MAC key exist as the two `Secret`s this object hands to the store, and
   * nowhere else in this module. */
  function snapshot() {
    return Object.freeze({
      state,
      code,
      project: scan === null ? null : scan.project,
      expiresAt: scan === null ? null : scan.expiresAt,
      deviceUid: signIn === null ? null : signIn.deviceUid,
      record,
      refusal,
    });
  }

  /**
   * Scan `text` and pair with the installation it names, under the owner's
   * label `id`.
   *
   * It is one call rather than five because the five have an order (§7.2) and
   * a caller between them could store, show or write the wrong thing. The
   * answer is the snapshot above: `watching` with a `code` on success, and a
   * refusal -- never a partial pairing, and never a stored byte -- otherwise.
   */
  async function start(text, { id } = {}) {
    try {
      return await begin(text, id);
    } catch {
      // A port that threw rather than refusing -- the Keychain, the database
      // client or the auth SDK failing at the platform. The session ends the
      // attempt like any other refusal, and deliberately does not carry the
      // platform's message: it may quote a value.
      return refusedWith(refuse(SESSION_CODES.PORT_FAILED));
    }
  }

  async function begin(text, id) {
    // Only a pairing already in flight is refused. A refused, expired or
    // delivered session may be started again -- a second QR, a re-pair -- and
    // every attempt begins from nothing: a new key, a new code, a new watch.
    if (state === SESSION_STATES.WATCHING) return refuse(SESSION_CODES.ALREADY_STARTED);
    reset();
    // 1. The payload, judged before anything is stored (`payload.js`). A
    //    refusal here has no side effect at all: no key drawn, no node written,
    //    no code shown.
    const scanned = readScannedPayload(text);
    if (!scanned.ok) return refusedWith(scanned);
    // The label is judged here, before the request node exists: `account.js`
    // owns the rule (an id is a storage key segment), and a label the store
    // would refuse must not leave a pairing half-done in the tree.
    if (!isAccountId(id)) return refusedWith(refuse(SESSION_CODES.BAD_ACCOUNT, 'id'));
    scan = scanned.payload;
    accountId = id;
    // 2. The key pair, on the phone, from the phone's own randomness (D165:
    //    the code is the key's fingerprint, so the key must be this phone's).
    const pair = generateKeyPair({ x25519, random });
    if (!pair.ok) return refusedWith(pair);
    secret = pair.secret;
    publicKey = pair.publicKey;
    // 3. The code: derived, held here, never stored, never sent. The operator's
    //    CLI derives it from the key it read off the request node, and the two
    //    matching is the whole of M3.
    code = fingerprint(publicKey);
    // 4. The request, written **once** (§7.2). This is the only thing the phone
    //    puts in the tree, and the rules give the pairing uid exactly this child
    //    and the delivery.
    const written = await database.set(requestPath(scan.uid), {
      key: encodeDeviceKey(publicKey),
      at: instant(clock.nowMs()),
    });
    if (!written || written.ok !== true) return refusedWith(refuse(SESSION_CODES.REQUEST_REFUSED, 'request'));
    // 5. Sign in with the pairing token from the QR: it reaches two nodes and
    //    nothing else (§4.4), and the uid it authenticates as is the pairing's
    //    own name -- an Auth answer naming another identity is not ours to use.
    const signed = await auth.signIn({
      apiKey: scan.apiKey,
      databaseUrl: scan.databaseUrl,
      customToken: scan.token,
    });
    if (!signed || signed.ok !== true) return refusedWith(refuse(SESSION_CODES.SIGN_IN_REFUSED, 'token'));
    if (signed.uid !== scan.uid) return refusedWith(refuse(SESSION_CODES.IDENTITY_MISMATCH, 'uid'));
    signIn = Object.freeze({ deviceUid: null });
    // 6. The watch, attached before anything else can happen, because the
    //    delivery and the delete are two events and only a client that is
    //    watching sees the first (§7.2's amendment).
    const watching = database.watch(pairingPath(scan.uid), onValue, onError);
    if (!watching || watching.ok !== true || typeof watching.stop !== 'function') {
      return refusedWith(refuse(SESSION_CODES.WATCH_REFUSED, 'watch'));
    }
    stop = watching.stop;
    state = SESSION_STATES.WATCHING;
    return Object.freeze({ ok: true, session: snapshot() });
  }

  /** The listener's value: the pairing node, as the CLI left it. */
  async function onValue(value) {
    if (state !== SESSION_STATES.WATCHING) return;
    if (value === null || value === undefined) {
      // The subtree is gone and no delivery was ever seen: the CLI's wait
      // ended without a typed code, or the sweep removed the session. §7.2:
      // say the pairing expired.
      expired();
      return;
    }
    if (typeof value !== 'object' || typeof value.delivery !== 'string') return;
    try {
      await delivered(value.delivery);
    } catch {
      onError();
    }
  }

  /** The listener's error: a port that failed. The session says so rather than
   * waiting forever for a delivery that will not come. */
  function onError() {
    if (state !== SESSION_STATES.WATCHING) return;
    refusedWith(refuse(SESSION_CODES.WATCH_REFUSED, 'watch'));
  }

  /** §7.2's last three steps, on the delivery the CLI sealed. */
  async function delivered(text) {
    // Stop first: one delivery, one store, and no second value event can start
    // a second write while these awaits are in flight.
    if (stop !== null) {
      stop();
      stop = null;
    }
    const envelope = decodeEnvelope(text);
    if (!envelope.ok) return refusedWith(envelope);
    const opened = openDelivery({ envelope: envelope.bytes, secret, publicKey, uid: scan.uid, x25519 });
    if (!opened.ok) return refusedWith(opened);
    const judged = readDelivery({ plaintext: opened.plaintext, scan });
    if (!judged.ok) return refusedWith(judged);
    const { config, device, macKey } = judged.delivery;
    // 7. The account's address, then its two items -- and only these. The
    //    tuple is D114's ("the app stores the tuples, never a credential") and
    //    the items are D111's two, written through the store's own door so the
    //    value never exists outside a `Secret`.
    const added = await store.add({
      id: accountId,
      firebaseProjectId: config.gcpProject,
      authUid: device.uid,
      fugaroProject: config.project,
    });
    if (!added.ok) return refusedWith(added);
    for (const [item, value] of [
      ['idToken', device.token],
      ['macKey', encodeBase64url(macKey)],
    ]) {
      const made = makeSecret(item, value);
      if (!made.ok) return refusedWith(refuse(SESSION_CODES.STORE_REFUSED, item));
      const written = await store.setSecret(accountId, item, made.secret);
      if (!written.ok) {
        // The tuple exists and an item did not land. That is the state
        // `account.js` calls the safe one -- an account whose secret is missing
        // fails closed the moment it is used, unlike a credential with no owner
        // -- so nothing is rolled back here: the refusal says which item, and
        // re-pairing is the repair.
        return refusedWith(refuse(SESSION_CODES.STORE_REFUSED, item));
      }
    }
    // 8. The record, from the delivery and not the scan (D199). It is held for
    //    the pairing and not persisted: this build has no item for it (the
    //    header says why).
    record = Object.freeze({
      project: config.project,
      gcpProject: config.gcpProject,
      databaseUrl: config.databaseUrl,
      apiKey: config.apiKey,
      deviceUid: device.uid,
    });
    signIn = Object.freeze({ deviceUid: device.uid });
    state = SESSION_STATES.DELIVERED;
    return Object.freeze({ ok: true, session: snapshot() });
  }

  /**
   * Give up: stop the listener and forget the key. The pairing node is the
   * CLI's to delete, and the phone has nothing to write.
   */
  function cancel() {
    if (stop !== null) {
      stop();
      stop = null;
    }
    reset();
    state = SESSION_STATES.IDLE;
    return snapshot();
  }

  function expired() {
    if (stop !== null) {
      stop();
      stop = null;
    }
    // The attempt is over, so the private key goes with it: nothing will open a
    // delivery under it, and §7.2's answer to a late read is to pair again --
    // with a new key and a new code.
    secret = null;
    publicKey = null;
    state = SESSION_STATES.EXPIRED;
    refusal = Object.freeze({ ok: false, code: SESSION_CODES.EXPIRED });
    return snapshot();
  }

  function refusedWith(answer) {
    if (stop !== null) {
      stop();
      stop = null;
    }
    // The key goes with the attempt: a refused pairing must leave nothing that
    // could open a delivery later, and the next `start` draws a new pair.
    secret = null;
    publicKey = null;
    state = SESSION_STATES.REFUSED;
    refusal = Object.freeze({ ok: false, code: answer.code, ...(answer.field === undefined ? {} : { field: answer.field }) });
    return refusal;
  }

  /** Forget the attempt's own material -- the private key above all, which
   * lives in this object for the length of one pairing and dies with it. The
   * two stored items are the account's, not the attempt's, and are what a
   * re-pair keeps. */
  function reset() {
    secret = null;
    publicKey = null;
    code = null;
    scan = null;
    accountId = null;
    refusal = null;
    record = null;
    signIn = null;
  }

  return Object.freeze({ start, cancel, snapshot });
}

