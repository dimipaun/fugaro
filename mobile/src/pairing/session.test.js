// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The pairing session, step by step (mobile plan, Task P6.8; the pairing
// plan's §7.2 and §4.4).
//
// This is the file that pins P6.8's own verification bullets, one test each:
//
//   * "a scanned payload is validated before anything is stored" -- a refused
//     scan writes no node, signs nobody in, draws no key and stores nothing;
//   * "the key pair is generated on the phone and the code shown is its
//     fingerprint" -- the request node's `key` is the port's public key and the
//     snapshot's `code` is `fingerprint` of it;
//   * "the delivered device token and MAC key are decrypted and go only to
//     secure storage" -- both items are in the Keychain and neither value is
//     anywhere else: not in the snapshot, not in the settings store, not in the
//     session's own state;
//   * "the code shown is derived per pairing" -- two pairings, two keys, two
//     codes, and no code stored.
//
// The wire shapes the CLI and the rules already fix are pinned here as well,
// because the session is what writes and reads them: the request's `at` is the
// whole number the rules' `TIMESTAMP` macro takes and not a string, the request
// is written **after** the token is exchanged (the write is authenticated,
// §2 (a)), the listener is on the delivery child (the one path a pairing token
// may read), and a delivery arrives inside the CLI's node -- `{v, sealed}` --
// whose version is judged before the envelope is. Each of those was a way for
// the session to be wrong while every other test passed.
//
// The delivery is sealed with `node:crypto`, as in `envelope.test.js`, so the
// test's own implementation is OpenSSL's and the app's X25519 is a port. Every
// value is synthetic: the token and the MAC key are shaped like nothing.

import assert from 'node:assert/strict';
import { createCipheriv, createPrivateKey, createPublicKey, diffieHellman, hkdfSync } from 'node:crypto';
import test from 'node:test';

import { ResourceRegistry } from '../accounts/resources.js';
import { MemorySecureStore, SecretStore } from '../accounts/secure-store.js';
import { MemorySettingsStore, SettingsStore } from '../accounts/settings.js';
import { AccountStore } from '../accounts/store.js';

import { encodeBase64url } from './base64url.js';
import { DELIVERY_INFO, DELIVERY_VERSION, ENVELOPE_CODES, deliveryAAD } from './envelope.js';
import { encodeDeviceKey, fingerprint } from './key.js';
import { PAYLOAD_VERSION } from './payload.js';
import { SESSION_CODES, SESSION_STATES, createPairingSession, deliveryPath, requestPath } from './session.js';

/** The pairing's uid, in the one canonical spelling. */
const UID = 'p~' + 'A'.repeat(43);

/** The phone's clock, and the deadline the QR carries: ten minutes after it, so
 * every test starts inside the pairing's own deadline unless it says otherwise.
 * The deadline is the instant the QR names -- the pairing token's own `exp` --
 * and not the test's invention. */
const T0 = Date.UTC(2026, 10, 11, 17, 40, 0);
const DEADLINE = '2026-11-11T17:50:00Z';
const DEADLINE_MS = Date.parse(DEADLINE);

/** The installation the QR names: `internal/cli/pair.go`'s `pairQR` shape with
 * this file's values. */
function scanned(over = {}) {
  return JSON.stringify({
    v: PAYLOAD_VERSION,
    project: 'aurora',
    gcp_project: 'fp-1234',
    database_url: 'https://fp-1234-default-rtdb.firebaseio.com',
    api_key: 'test-web-api-key',
    token: 'test-pairing-token-not-a-credential',
    uid: UID,
    expires_at: DEADLINE,
    ...over,
  });
}

/** The delivery `token.MarshalPayload` writes, with this file's values. */
function payloadOf(over = {}) {
  return {
    config: {
      project: 'aurora',
      gcp_project: 'fp-1234',
      database_url: 'https://fp-1234-default-rtdb.firebaseio.com',
      api_key: 'test-web-api-key',
    },
    device: { uid: 'm~' + 'M'.repeat(21) + 'A', token: 'test-device-token-not-a-credential' },
    mac_key: 'K'.repeat(42) + 'A',
    ...over,
  };
}

const PKCS8 = '302e020100300506032b656e04220420';
const SPKI = '302a300506032b656e032100';

function hex(bytes) {
  return [...bytes].map((byte) => byte.toString(16).padStart(2, '0')).join('');
}

function privateKey(secret) {
  return createPrivateKey({ key: Buffer.from(PKCS8 + hex(secret), 'hex'), format: 'der', type: 'pkcs8' });
}

function publicKey(bytes) {
  return createPublicKey({ key: Buffer.from(SPKI + hex(bytes), 'hex'), format: 'der', type: 'spki' });
}

/** The app's X25519 dependency, as a port, backed by OpenSSL. */
const nodeX25519 = Object.freeze({
  getPublicKey(secret) {
    const der = createPublicKey(privateKey(secret)).export({ format: 'der', type: 'spki' });
    return new Uint8Array(der.subarray(der.length - 32));
  },
  getSharedSecret(secret, peer) {
    return new Uint8Array(diffieHellman({ privateKey: privateKey(secret), publicKey: publicKey(peer) }));
  },
});

/** A deterministic draw, so a test can name the key the session will hold:
 * `key.js`'s port is the function itself. */
function randomPort(byte) {
  return (count) => new Uint8Array(count).fill(byte);
}

/** Seal a delivery for `phonePublic`, with OpenSSL: the CLI's own layout. */
function seal({ phonePublic, uid, payload, ephemeralSecret }) {
  const shared = nodeX25519.getSharedSecret(ephemeralSecret, phonePublic);
  const key = new Uint8Array(hkdfSync('sha256', Buffer.from(shared), Buffer.alloc(0), Buffer.from(DELIVERY_INFO), 32));
  const nonce = new Uint8Array(12).fill(7);
  const cipher = createCipheriv('aes-256-gcm', Buffer.from(key), Buffer.from(nonce));
  cipher.setAAD(Buffer.from(deliveryAAD(uid, phonePublic, DELIVERY_VERSION)));
  const text = Buffer.from(JSON.stringify(payload));
  const body = Buffer.concat([cipher.update(text), cipher.final()]);
  const envelope = new Uint8Array(1 + 32 + 12 + body.length + 16);
  envelope[0] = DELIVERY_VERSION;
  envelope.set(nodeX25519.getPublicKey(ephemeralSecret), 1);
  envelope.set(nonce, 33);
  envelope.set(new Uint8Array(body), 45);
  envelope.set(new Uint8Array(cipher.getAuthTag()), 45 + body.length);
  return encodeBase64url(envelope);
}

/** The node `pair.go` writes at `pairing/$uid/delivery` (`pairDeliveryNode`):
 * the version and the sealed envelope, and nothing else. The phone watches this
 * node, so a delivery reaches the session inside it and never bare. */
function deliveryNode(sealed, over = {}) {
  return { v: DELIVERY_VERSION, sealed, ...over };
}

/** The three stores, and the raw ports a test inspects. */
function makeStores() {
  const secretPort = new MemorySecureStore();
  const settingsPort = new MemorySettingsStore();
  const store = new AccountStore({
    secrets: new SecretStore(secretPort),
    settings: new SettingsStore(settingsPort),
    registry: new ResourceRegistry(),
  });
  return { secretPort, settingsPort, store };
}

/** The database port: records every write, holds one watch, and lets a test
 * push a value at it the way Firebase would. */
function makeDatabase({ failSet = false, failWatch = false } = {}) {
  const writes = [];
  const watched = [];
  let live = null;
  return {
    writes,
    watched,
    set(path, value) {
      writes.push({ path, value });
      if (failSet) return Promise.resolve({ ok: false, code: 'refused' });
      return Promise.resolve({ ok: true });
    },
    watch(path, onValue, onError) {
      if (failWatch) return { ok: false, code: 'refused' };
      const entry = { path, onValue, onError, stopped: false };
      watched.push(entry);
      live = entry;
      return {
        ok: true,
        stop() {
          entry.stopped = true;
          if (live === entry) live = null;
        },
      };
    },
    /** What Firebase does: dispatch the value to whoever is watching. */
    async push(value) {
      const entry = live;
      if (entry === null) return false;
      await entry.onValue(value);
      return true;
    },
    pushError() {
      if (live !== null) live.onError();
    },
    get stopped() {
      return watched.filter((entry) => entry.stopped).length;
    },
  };
}

function makeSession({ random = randomPort(9), database = makeDatabase(), auth, stores = makeStores(), clock = { nowMs: () => T0 } } = {}) {
  const signedIn = [];
  const authPort = auth ?? {
    signIn(call) {
      signedIn.push(call);
      return Promise.resolve({ ok: true, uid: UID, idToken: 'test-pairing-id-token' });
    },
  };
  const session = createPairingSession({
    x25519: nodeX25519,
    random,
    clock,
    database,
    auth: authPort,
    store: stores.store,
  });
  return { session, database, stores, signedIn };
}

test('a refused scan stores nothing: no write, no sign-in, no key, no code', async () => {
  const { session, database, stores, signedIn } = makeSession();
  const refused = await session.start(scanned({ database_url: 'https://evil.example/' }), { id: 'laptop' });
  assert.equal(refused.ok, false);
  assert.equal(refused.code, 'bad-database-url');
  assert.deepEqual(database.writes, [], 'a refused payload must not reach the tree');
  assert.deepEqual(database.watched, [], 'and must not open a listener');
  assert.deepEqual(signedIn, [], 'and must not sign anybody in');
  assert.deepEqual(await stores.store.list().then((r) => r.accounts), [], 'and must not store an account');
  assert.deepEqual(stores.secretPort.keys(), [], 'and must not write an item');
  // The snapshot is the screen's whole view of the attempt, and it carries
  // neither a code nor a key.
  const shown = session.snapshot();
  assert.equal(shown.state, SESSION_STATES.REFUSED);
  assert.equal(shown.code, null);
  assert.equal(shown.project, null);
});

test('the key pair is the phone’s, and the code is its fingerprint', async () => {
  const { session, database } = makeSession({ random: randomPort(0x2a) });
  const started = await session.start(scanned(), { id: 'laptop' });
  assert.equal(started.ok, true, JSON.stringify(started));
  assert.equal(started.session.state, SESSION_STATES.WATCHING);
  // The pair was drawn from the phone's own randomness, and nothing else.
  const phonePublic = nodeX25519.getPublicKey(new Uint8Array(32).fill(0x2a));
  assert.equal(started.session.code, fingerprint(phonePublic));
  assert.equal(started.session.project, 'aurora');
  // The request node is §7.2's: the public key in its one pinned spelling, and
  // the instant. Never the private half, never the code.
  assert.equal(database.writes.length, 1);
  const [request] = database.writes;
  assert.equal(request.path, requestPath(UID));
  assert.deepEqual(Object.keys(request.value).sort(), ['at', 'key']);
  assert.equal(request.value.key, encodeDeviceKey(phonePublic));
  // The instant is the number the rules' `TIMESTAMP` macro validates and not a
  // string: `pairRequestNode.At` is a `json.Number` and `pairDecodeRequest`
  // refuses a quoted instant, so a quoted one is a request the CLI cannot read
  // and the deployed document will not take.
  assert.equal(request.value.at, T0);
  assert.equal(Number.isInteger(request.value.at), true);
  assert.equal(JSON.stringify(request.value).includes(started.session.code), false, 'the code is never written');
  // ... and the listener is on the delivery child, the one path the rules grant
  // a pairing token `.read` on.
  assert.equal(database.watched.length, 1);
  assert.equal(database.watched[0].path, deliveryPath(UID));
});

test('the token is exchanged before the request is written', async () => {
  const database = makeDatabase();
  const { session } = makeSession({
    database,
    auth: {
      signIn() {
        // The request node's rule is the pairing claim (§2 (a)), so the write is
        // an authenticated write: a phone that wrote first would be denied by
        // the deployed document and the pairing could never start.
        assert.deepEqual(database.writes, [], 'the request is written by an authenticated phone');
        return Promise.resolve({ ok: true, uid: UID });
      },
    },
  });
  const started = await session.start(scanned(), { id: 'laptop' });
  assert.equal(started.ok, true, JSON.stringify(started));
  assert.equal(database.writes.length, 1, 'and it is written exactly once');
  assert.equal(database.writes[0].path, requestPath(UID));
});

test('a delivery node this build cannot read is refused whole', async () => {
  const phonePublic = nodeX25519.getPublicKey(new Uint8Array(32).fill(0x55));
  const sealed = seal({ phonePublic, uid: UID, payload: payloadOf(), ephemeralSecret: new Uint8Array(32).fill(9) });
  const cases = [
    // The envelope on its own is not a delivery: the CLI writes it inside the
    // node, and a listener on the delivery child is handed the node.
    ['the envelope without its node', sealed, ENVELOPE_CODES.NOT_A_NODE],
    ['a version from a later format', deliveryNode(sealed, { v: DELIVERY_VERSION + 1 }), ENVELOPE_CODES.BAD_VERSION],
    ['a version that is not a number', deliveryNode(sealed, { v: String(DELIVERY_VERSION) }), ENVELOPE_CODES.BAD_VERSION],
    ['a field this build does not know', { ...deliveryNode(sealed), extra: 1 }, ENVELOPE_CODES.UNKNOWN_FIELD],
    ['a node with no envelope', { v: DELIVERY_VERSION }, ENVELOPE_CODES.MISSING_FIELD],
    ['an envelope that is not text', { v: DELIVERY_VERSION, sealed: 5 }, ENVELOPE_CODES.NOT_TEXT],
  ];
  for (const [what, node, code] of cases) {
    const { session, database, stores } = makeSession({ random: randomPort(0x55) });
    await session.start(scanned(), { id: 'laptop' });
    await database.push(node);
    const shown = session.snapshot();
    assert.equal(shown.state, SESSION_STATES.REFUSED, what);
    assert.equal(shown.refusal.code, code, what);
    assert.deepEqual(stores.secretPort.keys(), [], what);
  }
});

test('the delivered token and MAC key go to secure storage and nowhere else', async () => {
  const { session, database, stores } = makeSession({ random: randomPort(0x11) });
  const started = await session.start(scanned(), { id: 'laptop' });
  const phonePublic = nodeX25519.getPublicKey(new Uint8Array(32).fill(0x11));
  const payload = payloadOf();
  const delivered = await database.push(
    deliveryNode(seal({ phonePublic, uid: UID, payload, ephemeralSecret: new Uint8Array(32).fill(5) })),
  );
  assert.equal(delivered, true);
  const shown = session.snapshot();
  assert.equal(shown.state, SESSION_STATES.DELIVERED, JSON.stringify(shown));
  assert.equal(database.stopped, 1, 'one delivery, one listener, stopped');

  // The two items, in the Keychain, under the account's own names.
  const items = stores.secretPort.keys();
  assert.deepEqual(items, ['fugaro.account.laptop.idToken', 'fugaro.account.laptop.macKey']);
  const token = await stores.store.secret('laptop', 'idToken');
  const mac = await stores.store.secret('laptop', 'macKey');
  assert.equal(token.ok, true);
  assert.equal(mac.ok, true);
  assert.equal(token.secret.reveal(), payload.device.token);
  assert.equal(mac.secret.reveal(), payload.mac_key);

  // The account tuple is the delivery's, and it is D114's four fields.
  const listed = await stores.store.list();
  assert.deepEqual(
    { ...listed.accounts[0] },
    { id: 'laptop', firebaseProjectId: 'fp-1234', authUid: payload.device.uid, fugaroProject: 'aurora' },
  );

  // **Nowhere else.** Neither value is in the screen's view, in the settings
  // store, or in anything the session hands back -- and the settings store
  // refuses an API-key-shaped value outright, which is why the configuration
  // is not there (the module header says what is deferred).
  const whole = JSON.stringify(shown) + JSON.stringify(await stores.settingsPort.keys());
  assert.equal(whole.includes(payload.device.token), false);
  assert.equal(whole.includes(payload.mac_key), false);
  assert.equal(shown.deviceUid, payload.device.uid, 'the uid is a key and not a secret (§4.5)');
  assert.equal(shown.record.databaseUrl, payload.config.database_url);
  assert.equal(shown.record.apiKey, payload.config.api_key);
});

test('the code is derived per pairing and stored nowhere', async () => {
  const first = makeSession({ random: randomPort(1) });
  const second = makeSession({ random: randomPort(2) });
  const one = await first.session.start(scanned(), { id: 'laptop' });
  const two = await second.session.start(scanned(), { id: 'laptop' });
  assert.equal(one.ok && two.ok, true);
  assert.notEqual(one.session.code, two.session.code, 'a fresh key pair is a fresh code');
  // The same installation, paired twice, is two codes and two request keys:
  // the code is a fingerprint of a key, not a property of the project.
  assert.notEqual(first.database.writes[0].value.key, second.database.writes[0].value.key);
  // Neither the code nor the key is in a store.
  assert.deepEqual(first.stores.secretPort.keys(), []);
  assert.deepEqual([...(await first.stores.settingsPort.keys())], []);
});

test('a delivery that disagrees with the scan is refused whole', async () => {
  const { session, database, stores } = makeSession({ random: randomPort(0x33) });
  await session.start(scanned(), { id: 'laptop' });
  const phonePublic = nodeX25519.getPublicKey(new Uint8Array(32).fill(0x33));
  const payload = payloadOf({
    config: { ...payloadOf().config, project: 'other', gcp_project: 'fp-9999', database_url: 'https://fp-9999-default-rtdb.firebaseio.com' },
  });
  await database.push(deliveryNode(seal({ phonePublic, uid: UID, payload, ephemeralSecret: new Uint8Array(32).fill(6) })));
  const shown = session.snapshot();
  assert.equal(shown.state, SESSION_STATES.REFUSED);
  assert.equal(shown.refusal.code, 'disagrees-with-scan');
  assert.deepEqual(stores.secretPort.keys(), [], 'nothing is stored');
  assert.deepEqual([...(await stores.store.list()).accounts], [], 'and no account is added');
});

test('a null on the delivery child is passed over, and the deadline is what ends the wait', async () => {
  // The delivery child does not exist for the whole of the operator's typing,
  // and the subtree's deletion looks exactly the same from it: the rules give
  // the phone no read on the parent to tell those apart, so neither may end the
  // session -- a session that gave up on a null would give up the instant it
  // started.
  let now = T0;
  const { session, database, stores } = makeSession({ clock: { nowMs: () => now } });
  await session.start(scanned(), { id: 'laptop' });
  await database.push(null);
  assert.equal(session.snapshot().state, SESSION_STATES.WATCHING, 'no delivery yet is not an ending');
  // A tick inside the deadline changes nothing either.
  now = DEADLINE_MS - 1;
  assert.equal(session.tick().state, SESSION_STATES.WATCHING);
  // The deadline itself is the end of it: `expires_at` is the pairing token's
  // own `exp`, so nothing can arrive after it.
  now = DEADLINE_MS;
  const shown = session.tick();
  assert.equal(shown.state, SESSION_STATES.EXPIRED);
  assert.equal(shown.refusal.code, SESSION_CODES.EXPIRED);
  assert.equal(database.stopped, 1, 'the listener is stopped');
  assert.deepEqual(stores.secretPort.keys(), []);
  // ... and the code path is still there to be shown again, with no key behind
  // it: a re-scan draws a new pair.
  assert.equal(typeof shown.code, 'string');
});

test('a scan whose deadline has gone is expired before a key is drawn', async () => {
  const { session, database, signedIn, stores } = makeSession({ clock: { nowMs: () => DEADLINE_MS } });
  const expired = await session.start(scanned(), { id: 'laptop' });
  assert.equal(expired.ok, false);
  assert.equal(expired.code, SESSION_CODES.EXPIRED);
  assert.equal(session.snapshot().state, SESSION_STATES.EXPIRED);
  // The token in that QR died with the deadline, so there is nothing to
  // exchange and no request node worth writing: the pairing leaves no orphan
  // for the sweep and no code on screen.
  assert.deepEqual(signedIn, []);
  assert.deepEqual(database.writes, []);
  assert.deepEqual(database.watched, []);
  assert.deepEqual(stores.secretPort.keys(), []);
  assert.equal(session.snapshot().code, null);
});

test('the session refuses to pair twice at once, and cancels cleanly', async () => {
  const { session, database, stores } = makeSession();
  await session.start(scanned(), { id: 'laptop' });
  const again = await session.start(scanned(), { id: 'laptop' });
  assert.equal(again.ok, false);
  assert.equal(again.code, SESSION_CODES.ALREADY_STARTED);
  assert.equal(database.writes.length, 1, 'the request node is written once');
  session.cancel();
  assert.equal(database.stopped, 1, 'cancelling stops the listener');
  assert.equal(session.snapshot().state, SESSION_STATES.IDLE);
  // A cancelled pairing stored nothing, and a second scan is a fresh attempt.
  assert.deepEqual(stores.secretPort.keys(), []);
  const restarted = await session.start(scanned(), { id: 'laptop' });
  assert.equal(restarted.ok, true);
  assert.equal(database.writes.length, 2);
});

test('a second Pair before the first finishes is refused, not interleaved', async () => {
  // The Pair button is drawn for every state but `watching` (`screen.js`), so a
  // double-tap arrives while the first attempt is suspended on the exchange.
  // The two would share this object's variables: the second press would reset
  // the first's key and draw its own, the first would write the second one's
  // public key to the request node, and the second's write would be denied by
  // that node's own rule (`!data.exists()`) -- so the loser's refusal would
  // stop the winner's listener and leave a node behind that nothing can pair
  // against again, because the request is written exactly once.
  const auth = {
    async signIn() {
      // An exchange that does not answer in the same turn, which is what a
      // network round trip is: `begin` is suspended here when the second press
      // arrives.
      await null;
      return { ok: true, uid: UID };
    },
  };
  // A draw per key pair, so the test can name whose key reached the tree: if
  // the second press got as far as drawing, it is the second draw that is
  // written and the first attempt's key is lost.
  const draws = [0x21, 0x22];
  const { session, database } = makeSession({ auth, random: (count) => new Uint8Array(count).fill(draws.shift()) });
  const first = session.start(scanned(), { id: 'laptop' });
  const second = await session.start(scanned(), { id: 'laptop' });
  assert.equal(second.ok, false);
  assert.equal(second.code, SESSION_CODES.ALREADY_STARTED);
  assert.equal(session.snapshot().state, SESSION_STATES.STARTING, 'the first attempt is in flight');
  // §7.2's order: the code is shown before the token is exchanged, so it is on
  // screen for the whole of the write.
  assert.equal(typeof session.snapshot().code, 'string');

  const started = await first;
  assert.equal(started.ok, true, JSON.stringify(started));
  assert.equal(started.session.state, SESSION_STATES.WATCHING);
  assert.equal(database.writes.length, 1, 'one press, one request node');
  assert.equal(database.watched.length, 1, 'and one listener');
  assert.equal(database.watched[0].path, deliveryPath(UID));
  assert.equal(session.snapshot().state, SESSION_STATES.WATCHING);
  // The node carries the first press's key, and the second press drew none:
  // the two attempts never shared a variable.
  assert.equal(database.writes[0].value.key, encodeDeviceKey(nodeX25519.getPublicKey(new Uint8Array(32).fill(0x21))));
  assert.deepEqual(draws, [0x22], 'the second press never reached the key pair');
});

test('a Cancel or a new scan cannot land between the delivery and its store', async () => {
  // The delivery's two writes are awaits, and Cancel is drawn while a session
  // is `watching` (`screen.js`), so the window is a real one. Without a state
  // of its own for it, a Cancel there resets the account id and a new scan sets
  // a new one -- and this delivery's token and MAC key land under the *new*
  // account, which is a credential under the wrong owner, with the first
  // account's tuple left behind and no secret in it.
  const stores = makeStores();
  // A store that does not answer in the same turn, so the window can be
  // entered: every other port here is a promise already resolved.
  const slow = {
    add: (account) => Promise.resolve().then(() => stores.store.add(account)),
    setSecret: (id, item, secret) => Promise.resolve().then(() => stores.store.setSecret(id, item, secret)),
    list: () => stores.store.list(),
    secret: (id, item) => stores.store.secret(id, item),
  };
  const { session, database } = makeSession({ stores: { ...stores, store: slow }, random: randomPort(0x11) });
  await session.start(scanned(), { id: 'laptop' });
  const phonePublic = nodeX25519.getPublicKey(new Uint8Array(32).fill(0x11));
  const payload = payloadOf();
  const delivered = database.push(
    deliveryNode(seal({ phonePublic, uid: UID, payload, ephemeralSecret: new Uint8Array(32).fill(5) })),
  );

  // The delivery is in and the store has not answered: this is the window.
  assert.equal(session.snapshot().state, SESSION_STATES.STORING);
  assert.equal(session.cancel().state, SESSION_STATES.STORING, 'a session that is storing is not cancellable');
  const again = await session.start(scanned(), { id: 'tablet' });
  assert.equal(again.ok, false);
  assert.equal(again.code, SESSION_CODES.ALREADY_STARTED);
  assert.deepEqual(database.writes, [database.writes[0]], 'and the second scan wrote nothing');

  await delivered;
  assert.equal(session.snapshot().state, SESSION_STATES.DELIVERED);
  // Everything landed under the account the delivery was for, and nothing
  // landed under the second label.
  assert.deepEqual(
    [...(await stores.store.list()).accounts].map((account) => account.id),
    ['laptop'],
  );
  const token = await stores.store.secret('laptop', 'idToken');
  const mac = await stores.store.secret('laptop', 'macKey');
  assert.equal(token.ok, true);
  assert.equal(mac.ok, true);
  assert.equal(token.secret.reveal(), payload.device.token);
  assert.equal(mac.secret.reveal(), payload.mac_key);
  assert.equal((await stores.store.secret('tablet', 'idToken')).ok, false, 'nothing under the second label');
});

test('a store that throws ends the attempt rather than leaving it storing', async () => {
  // The store's writes are the only awaits inside the delivery, and a Keychain
  // that throws rather than refusing must not leave the session `storing`
  // forever: a second `start` is refused there and `cancel` will not touch it,
  // so a throw would be a pairing with no way out.
  const stores = makeStores();
  const throwing = {
    add() {
      throw new Error('the keychain is unavailable');
    },
    setSecret: (id, item, secret) => stores.store.setSecret(id, item, secret),
    list: () => stores.store.list(),
    secret: (id, item) => stores.store.secret(id, item),
  };
  const { session, database } = makeSession({ stores: { ...stores, store: throwing }, random: randomPort(0x12) });
  await session.start(scanned(), { id: 'laptop' });
  const phonePublic = nodeX25519.getPublicKey(new Uint8Array(32).fill(0x12));
  await database.push(deliveryNode(seal({ phonePublic, uid: UID, payload: payloadOf(), ephemeralSecret: new Uint8Array(32).fill(4) })));
  const shown = session.snapshot();
  assert.equal(shown.state, SESSION_STATES.REFUSED);
  assert.equal(shown.refusal.code, SESSION_CODES.PORT_FAILED);
  assert.equal(database.stopped, 1, 'the listener goes with the attempt');
  // A refused attempt leaves the object usable: a re-pair is §7.2's repair.
  assert.equal((await session.start(scanned(), { id: 'laptop' })).ok, true);
});

test('a port that fails ends the attempt with a refusal, and stores nothing', async () => {
  // The request write failing is the case the ordering is for: no code is
  // shown to the operator for a pairing that cannot proceed.
  const refusedSet = makeSession({ database: makeDatabase({ failSet: true }) });
  const written = await refusedSet.session.start(scanned(), { id: 'laptop' });
  assert.equal(written.code, SESSION_CODES.REQUEST_REFUSED);
  // The code was derived before the write (§7.2's own order) and the screen
  // shows the refusal; what matters is that nothing was stored and no listener
  // was opened. The token was exchanged first -- the write is an authenticated
  // write (§2 (a)) -- so the exchange is the one step that did happen.
  assert.equal(refusedSet.session.snapshot().state, SESSION_STATES.REFUSED);
  assert.deepEqual(refusedSet.database.watched, []);
  assert.equal(refusedSet.signedIn.length, 1);
  assert.deepEqual(refusedSet.stores.secretPort.keys(), []);

  // The auth exchange failing leaves the listener unopened and nothing stored.
  const refusedAuth = makeSession({ auth: { signIn: () => Promise.resolve({ ok: false, code: 'refused' }) } });
  const signed = await refusedAuth.session.start(scanned(), { id: 'laptop' });
  assert.equal(signed.code, SESSION_CODES.SIGN_IN_REFUSED);
  assert.deepEqual(refusedAuth.database.watched, []);

  // An identity that is not the pairing's is not this phone's to use.
  const wrongIdentity = makeSession({ auth: { signIn: () => Promise.resolve({ ok: true, uid: 'p~' + 'Z'.repeat(43) }) } });
  const identity = await wrongIdentity.session.start(scanned(), { id: 'laptop' });
  assert.equal(identity.code, SESSION_CODES.IDENTITY_MISMATCH);

  // A watch that cannot be attached is a session that cannot receive the
  // delivery, so it refuses rather than waiting.
  const refusedWatch = makeSession({ database: makeDatabase({ failWatch: true }) });
  const watched = await refusedWatch.session.start(scanned(), { id: 'laptop' });
  assert.equal(watched.code, SESSION_CODES.WATCH_REFUSED);
});

test('a port that throws is a refusal and not a crash', async () => {
  const throwing = {
    writes: [],
    watched: [],
    set() {
      throw new Error('the database client is not configured');
    },
    watch() {
      return { ok: true, stop() {} };
    },
  };
  const { session } = makeSession({ database: throwing });
  const refused = await session.start(scanned(), { id: 'laptop' });
  assert.equal(refused.ok, false);
  assert.equal(refused.code, SESSION_CODES.PORT_FAILED);
  assert.equal(session.snapshot().state, SESSION_STATES.REFUSED);
});

test('the owner’s label is judged as a label, before anything is written', async () => {
  const { session, database } = makeSession();
  const bad = await session.start(scanned(), { id: 'not a label' });
  assert.equal(bad.ok, false);
  assert.equal(bad.code, SESSION_CODES.BAD_ACCOUNT);
  assert.deepEqual(database.writes, []);
  const missing = await session.start(scanned(), {});
  assert.equal(missing.code, SESSION_CODES.BAD_ACCOUNT);
});
