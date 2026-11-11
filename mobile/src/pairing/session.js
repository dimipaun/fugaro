// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The pairing session: §7.2's contract, in the order it is written (mobile
// plan, Task P6.8; the pairing plan's §7.2, §2 (a), §4.4, D111, D114, D165,
// D171, D199).
//
// **The order is the security property, so it is one function.** §7.2, as
// amended 2026-11-11 (the token and the write change places, and the reason is
// the paragraph after this one):
//
//     scan → validate before anything is stored → generate a key pair → show
//     the code derived from the public key → sign in with the pairing token →
//     write {key, at} to the request node once → watch `pairing/$uid/delivery`
//     → refuse the whole payload unless the envelope opens, the version is this
//     build's, and the AAD names this uid and this key → store the device token
//     and the MAC key in secure storage only → store the configuration from the
//     payload, not the scan (D199)
//
// **Why the token is exchanged before the request is written.** The request
// node's own rule is `PAIRING && newData.exists() && !data.exists()` (§2 (a)),
// where `PAIRING` is the pairing claim: `auth.token.pu == auth.uid`, on a `p~`
// uid. The write is therefore an authenticated write, and a phone that wrote
// first would be denied by the deployed document -- a pairing that can never
// start. Nothing in the exchange needs the node: the phone holds the pairing
// token from the QR and needs nothing from the CLI to use it. What §7.2's
// ordering is really about survives the move, because the write still happens
// before the operator can have typed anything (they type the code the CLI
// derived from the key in that node, so the node must exist first) and it is
// still written exactly once.
//
// Every step above is a line of `#start` or `#delivered` below, in that order,
// and each one refuses rather than continuing: a payload that is not a payload
// never reaches a key pair, a key pair whose sign-in failed never reaches the
// tree, and a delivery that does not open stores nothing at all. The whole
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
// **Why the watch is a listener, and why it is on the delivery child**
// (§7.2's amendment of 2026-10-30, T3b; the path corrected 2026-11-11). The CLI
// writes the delivery and deletes the whole `pairing/$uid` subtree in the next
// instant, because the payload must not sit readable for the hour the `p~` ID
// token lives. A one-shot read issued after sign-in races a window microseconds
// wide and loses it, so the contract is a `value` observer attached as soon as
// the token is exchanged, holding the first snapshot that carries a delivery.
//
// The observer is on `pairing/$uid/delivery` and not on the node above it: the
// merged rules grant `.read` at that child and nowhere else -- there is no
// `.read` at `pairing` or at `pairing/$uid`, so a listener above it is a
// default deny and every attempt would end `watch-refused` -- and P6.4's
// emulator fence reads exactly this child. The parent would have carried one
// extra signal, the delete, and that is the signal this node cannot carry:
//
// **A null is passed over, and the deadline is the phone's own.** On the
// delivery child a null means "no delivery yet" -- the ordinary case for the
// whole of the operator's typing, since the listener is attached before the CLI
// has written anything there -- and it is also what the delete of the subtree
// looks like. Those two are the same event seen from this node, so a session
// that read a null as an ending would end the instant it started. What ends a
// pairing nobody completed is the deadline the QR already carries: `expires_at`
// is the pairing token's own `exp` (`token.PairingLifetime` mints it as the
// CLI's wait plus slack), so no delivery can arrive after it and a phone past
// it is waiting for something that is not coming. `payload.js` reads that
// instant and does not judge it -- a late *delivery* is not refused for its
// lateness, because a delivery that arrived at all is a pairing that completed
// -- and this file is where the deadline is used: `start` ends a scan whose
// deadline has already gone, and `tick` ends a watch whose deadline goes by
// while it waits. There is no timer in this module (the caller ticks, as
// `voice-session/machine.js` says of its own), so the entry asks on each render.
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
import { openDelivery, readDeliveryNode } from './envelope.js';
import { encodeDeviceKey, fingerprint, generateKeyPair, isDriver } from './key.js';
import { readScannedPayload } from './payload.js';

/** The states a session is in, in the order it reaches them. */
export const SESSION_STATES = Object.freeze({
  IDLE: 'idle',
  STARTING: 'starting',
  WATCHING: 'watching',
  DELIVERED: 'delivered',
  REFUSED: 'refused',
  EXPIRED: 'expired',
});

/** How this module refuses, on top of the payload's and the delivery's codes:
 * a `scan` refusal is `payload.js`'s, a delivery refusal is `delivery.js`'s or
 * `envelope.js`'s, and a refusal of the session's own sequencing is here. */
export const SESSION_CODES = Object.freeze({
  PORT_FAILED: 'port-failed',
  BAD_ACCOUNT: 'bad-account',
  ALREADY_STARTED: 'already-started',
  SIGN_IN_REFUSED: 'sign-in-refused',
  IDENTITY_MISMATCH: 'identity-mismatch',
  REQUEST_REFUSED: 'request-refused',
  WATCH_REFUSED: 'watch-refused',
  STORE_REFUSED: 'store-refused',
  EXPIRED: 'expired',
});

/** What §7.2's request node holds: the phone's public key in its one pinned
 * spelling, and when it was written. */
export function requestPath(uid) {
  return `pairing/${uid}/request`;
}

/** What the phone reads: the delivery child and only it. The merged rules grant
 * `.read` there and nowhere above it, so this is the one path a pairing token
 * can watch (the module header's reason). */
export function deliveryPath(uid) {
  return `pairing/${uid}/delivery`;
}

function refuse(code, field) {
  return Object.freeze({ ok: false, code, ...(field === undefined ? {} : { field }) });
}

/** The instant in the one spelling the request node's rule takes: a
 * non-negative whole number of epoch milliseconds (`TIMESTAMP` in the rules is
 * `newData.isNumber() && newData.val() % 1 == 0`). `pair.go`'s
 * `pairRequestNode.At` is a `json.Number` for the same reason and
 * `pairDecodeRequest` refuses a quoted instant, so a clock port that answers
 * anything else is a port that failed, not a value to spell differently. */
function epochMs(clock) {
  const now = clock.nowMs();
  if (typeof now !== 'number' || !Number.isInteger(now) || now < 0) return null;
  return now;
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
 *   * `clock` -- `nowMs()`, for the request's `at` and for the deadline the
 *     scan's `expires_at` names, and for nothing else;
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
    //
    // **In flight includes the exchange and the write**, and that is the whole
    // point of the `starting` state: the steps below are one `begin`'s, over
    // this object's own variables, and two of them at once interleave rather
    // than queue. The second `begin` would reset the first's key and draw its
    // own, the first would write the *second* one's public key to the request
    // node, and the second's write would be denied by that node's own rule
    // (`!data.exists()`) -- so the loser's refusal would stop the winner's
    // listener and leave a request node behind that nothing can pair against
    // again, because the request is written exactly once. A second press is a
    // real event and not a hypothetical: the Pair button is drawn for every
    // state but `watching` (`screen.js`), so a double-tap arrives while the
    // first attempt is suspended on the exchange.
    if (state === SESSION_STATES.STARTING || state === SESSION_STATES.WATCHING) {
      return refuse(SESSION_CODES.ALREADY_STARTED);
    }
    reset();
    // The attempt is in flight from here, before the first await, so a press
    // that arrives during it is refused above rather than racing it. Nothing
    // has happened yet that a person can see -- the payload has not been read,
    // no key exists and no node is written -- and every path out of the steps
    // below ends the attempt in a state of its own (`refused`, `expired`).
    state = SESSION_STATES.STARTING;
    // 1. The payload, judged before anything is stored (`payload.js`). A
    //    refusal here has no side effect at all: no key drawn, no node written,
    //    no code shown.
    const scanned = readScannedPayload(text);
    if (!scanned.ok) return refusedWith(scanned);
    // The deadline the QR carries is judged next, and before the key is drawn:
    // `expires_at` is the pairing token's own `exp`, so a scan past it holds a
    // token the exchange would refuse anyway, and the request node it would
    // have written is an orphan only the sweep removes. §7.2's answer for a
    // pairing that is over is "say the pairing expired".
    if (pastDeadline(scanned.payload)) return endExpired();
    // The label is judged here too, before the request node exists: `account.js`
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
    // 4. Sign in with the pairing token from the QR -- **before the write**,
    //    because the request node's rule is the pairing claim (§2 (a)). The
    //    token reaches two nodes and nothing else (§4.4), and the uid it
    //    authenticates as is the pairing's own name -- an Auth answer naming
    //    another identity is not ours to use.
    const signed = await auth.signIn({
      apiKey: scan.apiKey,
      databaseUrl: scan.databaseUrl,
      customToken: scan.token,
    });
    if (!signed || signed.ok !== true) return refusedWith(refuse(SESSION_CODES.SIGN_IN_REFUSED, 'token'));
    if (signed.uid !== scan.uid) return refusedWith(refuse(SESSION_CODES.IDENTITY_MISMATCH, 'uid'));
    signIn = Object.freeze({ deviceUid: null });
    // 5. The request, written **once** (§7.2), now that the token is exchanged
    //    and before the operator can have typed the code they read off it. This
    //    is the only thing the phone puts in the tree, and the rules give the
    //    pairing uid exactly this child and the delivery.
    const at = epochMs(clock);
    if (at === null) return refusedWith(refuse(SESSION_CODES.PORT_FAILED, 'clock'));
    const written = await database.set(requestPath(scan.uid), {
      key: encodeDeviceKey(publicKey),
      at,
    });
    if (!written || written.ok !== true) return refusedWith(refuse(SESSION_CODES.REQUEST_REFUSED, 'request'));
    // 6. The watch, attached before anything else can happen, because the
    //    delivery and the delete are two events and only a client that is
    //    watching sees the first (§7.2's amendment).
    const watching = database.watch(deliveryPath(scan.uid), onValue, onError);
    if (!watching || watching.ok !== true || typeof watching.stop !== 'function') {
      return refusedWith(refuse(SESSION_CODES.WATCH_REFUSED, 'watch'));
    }
    stop = watching.stop;
    state = SESSION_STATES.WATCHING;
    return Object.freeze({ ok: true, session: snapshot() });
  }

  /** The listener's value: the delivery node, as the CLI left it. */
  async function onValue(node) {
    if (state !== SESSION_STATES.WATCHING) return;
    // A null is the delivery child not existing yet -- the ordinary case for
    // the whole of the operator's typing -- and it is also what the subtree's
    // deletion looks like. The rules give the phone no read on the parent to
    // tell those apart, so neither is read as an ending here; the deadline is
    // (`tick`, and the module header's paragraph).
    if (node === null || node === undefined) return;
    const envelope = readDeliveryNode(node);
    if (!envelope.ok) return refusedWith(envelope);
    try {
      await delivered(envelope.bytes);
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
  async function delivered(envelope) {
    // Stop first: one delivery, one store, and no second value event can start
    // a second write while these awaits are in flight.
    if (stop !== null) {
      stop();
      stop = null;
    }
    const opened = openDelivery({ envelope, secret, publicKey, uid: scan.uid, x25519 });
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
   * Time passed. The caller ticks this -- there is no timer in this module --
   * and a watch whose deadline has gone ends here: §7.2's answer to a pairing
   * the operator never completed, and the one ending the delivery child cannot
   * report (the module header's paragraph). Nothing else changes state: a
   * session that is idle, delivered or refused is already over, and a watch
   * inside its deadline is still waiting.
   */
  function tick() {
    if (state !== SESSION_STATES.WATCHING) return snapshot();
    if (!pastDeadline(scan)) return snapshot();
    endExpired();
    return snapshot();
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

  /** Has the scan's own deadline gone? `payload.js` parsed `expires_at` into
   * epoch milliseconds and deliberately did not judge it; a clock that cannot
   * answer is not a deadline that has passed. */
  function pastDeadline(payload) {
    if (payload === null || typeof payload !== 'object') return false;
    const now = epochMs(clock);
    if (now === null) return false;
    return now >= payload.expiresAt;
  }

  function endExpired() {
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
    return refusal;
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

  return Object.freeze({ start, cancel, tick, snapshot });
}
