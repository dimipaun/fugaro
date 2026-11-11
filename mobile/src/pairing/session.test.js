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
import { DELIVERY_INFO, DELIVERY_VERSION, deliveryAAD } from './envelope.js';
import { encodeDeviceKey, fingerprint } from './key.js';
import { PAYLOAD_VERSION } from './payload.js';
import { SESSION_CODES, SESSION_STATES, createPairingSession, pairingPath, requestPath } from './session.js';

/** The pairing's uid, in the one canonical spelling. */
const UID = 'p~' + 'A'.repeat(43);

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
    expires_at: '2026-11-11T17:50:00Z',
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

function makeSession({ random = randomPort(9), database = makeDatabase(), auth, stores = makeStores(), clock = { nowMs: () => 1_700_000_000_000 } } = {}) {
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
  assert.equal(request.value.at, '2023-11-14T22:13:20.000Z');
  assert.equal(JSON.stringify(request.value).includes(started.session.code), false, 'the code is never written');
  // ... and the listener is on the pairing node, which is what sees both the
  // delivery and the delete.
  assert.equal(database.watched.length, 1);
  assert.equal(database.watched[0].path, pairingPath(UID));
});

test('the delivered token and MAC key go to secure storage and nowhere else', async () => {
  const { session, database, stores } = makeSession({ random: randomPort(0x11) });
  const started = await session.start(scanned(), { id: 'laptop' });
  const phonePublic = nodeX25519.getPublicKey(new Uint8Array(32).fill(0x11));
  const payload = payloadOf();
  const delivered = await database.push({
    delivery: seal({ phonePublic, uid: UID, payload, ephemeralSecret: new Uint8Array(32).fill(5) }),
  });
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
  await database.push({ delivery: seal({ phonePublic, uid: UID, payload, ephemeralSecret: new Uint8Array(32).fill(6) }) });
  const shown = session.snapshot();
  assert.equal(shown.state, SESSION_STATES.REFUSED);
  assert.equal(shown.refusal.code, 'disagrees-with-scan');
  assert.deepEqual(stores.secretPort.keys(), [], 'nothing is stored');
  assert.deepEqual([...(await stores.store.list()).accounts], [], 'and no account is added');
});

test('a node that goes away before a delivery says the pairing expired', async () => {
  const { session, database, stores } = makeSession();
  await session.start(scanned(), { id: 'laptop' });
  await database.push(null);
  const shown = session.snapshot();
  assert.equal(shown.state, SESSION_STATES.EXPIRED);
  assert.equal(shown.refusal.code, SESSION_CODES.EXPIRED);
  assert.deepEqual(stores.secretPort.keys(), []);
  // ... and the code path is still there to be shown again, with no key behind
  // it: a re-scan draws a new pair.
  assert.equal(typeof shown.code, 'string');
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

test('a port that fails ends the attempt with a refusal, and stores nothing', async () => {
  // The request write failing is the case the ordering is for: no code is
  // shown to the operator for a pairing that cannot proceed.
  const refusedSet = makeSession({ database: makeDatabase({ failSet: true }) });
  const written = await refusedSet.session.start(scanned(), { id: 'laptop' });
  assert.equal(written.code, SESSION_CODES.REQUEST_REFUSED);
  // The code was derived before the write (§7.2's own order) and the screen
  // shows the refusal; what matters is that nothing was stored, no listener
  // was opened and nobody was signed in.
  assert.equal(refusedSet.session.snapshot().state, SESSION_STATES.REFUSED);
  assert.deepEqual(refusedSet.database.watched, []);
  assert.deepEqual(refusedSet.signedIn, []);
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
