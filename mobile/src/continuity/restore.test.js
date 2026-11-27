// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The silent restore: the challenge, the assertion, the session, and the one
// check O16 asks for (task K3; D2784, D2783, F15, O16).
//
// The red half of K3's second sentence is here too: **with the sync switch off
// the identity project is not called at all** -- the list comes from the
// phone's own keychain item -- and no project is restored until the challenge
// that project issued has been answered.
//
// The fingerprint is checked against Node's own SHA-256 and Node's own
// base64url rather than against this tree's encoder, which is what makes the
// test an independent reader of the one derivation O16 fixes
// (`devicekey.test.js` does the same with Ed25519).

import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import test from 'node:test';

import { assertSessionPort } from '../../../shared/mobile-session/port.js';
import { MemorySecureStore } from '../accounts/secure-store.js';
import { DeviceKeyStore } from '../session/devicekey.js';
import { ProjectSessionStore } from '../session/refresh.js';

import {
  CONSUMER_KEY_BYTES,
  RESTORE_CODES,
  consumerKeyFingerprint,
  parseChallengeResponse,
  parseRestoreResponse,
  requestRestoreChallenge,
  restoreAll,
  restoreIdentity,
  restoreProject,
} from './restore.js';
import { PairedListStore } from './sync.js';

const ENDPOINT = 'https://mobile-example.a.run.app';
const OTHER_ENDPOINT = 'https://other-example.a.run.app';
const IDENTITY_ENDPOINT = 'https://identity-example.a.run.app';
const ID_TOKEN = 'not-a-credential-test-id-token';
const IDENTITY = 'google.1234567890';
const PROJECT = 'fugaro-example';
const OTHER_PROJECT = 'other-example';
const CHALLENGE = 'not-a-credential-test-challenge';
const ASSERTION = 'not-a-credential-test-assertion';
const API_KEY = 'not-a-credential-test-api-key';
const TOKEN = 'not-a-credential-test-refresh-token';
const DATABASE_URL = 'https://example-default-rtdb.firebaseio.com';

const POLICY = Object.freeze({
  costly_modes: ['pro'],
  biometric_on_costly: false,
  compact_threshold_percent: 40,
  crash_reports: true,
});

/** The policy C2's port carries, which is C1's shape with the percent as a fraction. */
const PORT_POLICY = Object.freeze({
  costly_modes: Object.freeze(['pro']),
  biometric_on_costly: false,
  compaction_threshold: 0.4,
  crash_reports: true,
});

/** Unpadded base64url, from Node rather than from this tree's encoder. */
const base64url = (bytes) => Buffer.from(bytes).toString('base64url');

// Ed25519 stands in for the phone's: this module's logic needs a key that
// signs and a public key of the right size, not the arithmetic
// (`devicekey.test.js` checks the arithmetic against the platform's own). One
// fixed seed, so the expected public key and signature are writable below.
const SEED = new Uint8Array(32).fill(1);
const PUBLIC_KEY = new Uint8Array(32).fill(SEED[0] ^ 0x5a);
const ed25519 = Object.freeze({
  utils: Object.freeze({ randomSecretKey: () => new Uint8Array(SEED) }),
  getPublicKey: (seed) => new Uint8Array(32).fill(seed[0] ^ 0x5a),
  sign: () => new Uint8Array(64).fill(SEED[0]),
});

/** The consumer public key the fake project publishes, and the fingerprint O16 pins it by. */
const CONSUMER_KEY = new Uint8Array(CONSUMER_KEY_BYTES).fill(7);
const FINGERPRINT = consumerKeyFingerprint(base64url(CONSUMER_KEY)).fingerprint;

const ENTRY = Object.freeze({
  project_id: PROJECT,
  endpoint: ENDPOINT,
  identity: IDENTITY,
  consumer_key_fingerprint: FINGERPRINT,
  name: 'Example',
});

const OTHER_ENTRY = Object.freeze({
  ...ENTRY,
  project_id: OTHER_PROJECT,
  endpoint: OTHER_ENDPOINT,
  name: 'Other',
});

/** The session answer C1's `SessionResponse` names, as the service serialises it. */
function sessionAnswer(overrides = {}) {
  return JSON.stringify({
    project_id: PROJECT,
    endpoint: ENDPOINT,
    database_url: DATABASE_URL,
    api_key: API_KEY,
    role: 'operator',
    policy: POLICY,
    consumer_public_key: base64url(CONSUMER_KEY),
    refresh_token: TOKEN,
    custom_token: 'not-a-credential-test-custom-token',
    expires_in: 3600,
    ...overrides,
  });
}

/** A project's answers, recording every request it was given. */
function project({ restore = { status: 200, body: sessionAnswer() } } = {}) {
  const calls = [];
  const fetch = async (url, options = {}) => {
    calls.push({ url, method: options.method, headers: options.headers, body: options.body });
    if (url.endsWith('/mobile/v1/restore/challenge')) {
      return { status: 200, text: async () => JSON.stringify({ challenge: CHALLENGE, expires_in: 600 }) };
    }
    return { status: restore.status, text: async () => restore.body };
  };
  return { fetch, calls };
}

/** The identity project's client, as the app's entry would hand one in. */
function assertionOf(answer) {
  const asked = [];
  const assertionFor = async (request) => {
    asked.push(request);
    return answer;
  };
  return { assertionFor, asked };
}

function stores() {
  const keychain = new MemorySecureStore();
  return {
    keychain,
    deviceKey: new DeviceKeyStore({ port: keychain, ed25519 }),
    projectStore: new ProjectSessionStore(keychain),
    list: new PairedListStore({ port: keychain }),
  };
}

test('the fingerprint O16 pins a consumer key by is the hash of its bytes', () => {
  // The derivation is `internal/mailbox/kmssigner.go`'s `ConsumerKeyFingerprint`
  // and `internal/cli/pair_invite.go`'s pair half: SHA-256 of the **decoded**
  // key, unpadded base64url. Node's own crypto and Node's own base64url are the
  // second reader, so a change on either side of the phone is a failing test
  // rather than a QR whose fingerprint no phone could match.
  const pinned = consumerKeyFingerprint(base64url(CONSUMER_KEY));
  assert.equal(pinned.ok, true);
  assert.equal(pinned.fingerprint.length, 43);
  assert.equal(pinned.fingerprint, createHash('sha256').update(Buffer.from(CONSUMER_KEY)).digest('base64url'));
  // ... and it is the entry's, which is what `restoreProject` pins below.
  assert.equal(pinned.fingerprint, FINGERPRINT);

  // A key that is not that one spelling is refused rather than repaired: a
  // repaired key is a fingerprint over bytes the CLI never hashed.
  for (const bad of [
    `${base64url(CONSUMER_KEY)}==`,
    base64url(CONSUMER_KEY).slice(1),
    '+'.repeat(43),
    '!'.repeat(43),
    '',
    undefined,
    base64url(new Uint8Array(16)),
  ]) {
    assert.equal(consumerKeyFingerprint(bad).code, RESTORE_CODES.BAD_RESPONSE, String(bad));
  }
});

test('a restore answer is accepted only when the consumer key hashes to the entry’s fingerprint', () => {
  const good = parseRestoreResponse({ status: 200, body: sessionAnswer(), consumerKeyFingerprint: FINGERPRINT });
  assert.equal(good.ok, true);
  assert.equal(good.projectId, PROJECT);
  assert.equal(good.databaseUrl, DATABASE_URL);
  assert.equal(good.refreshToken, TOKEN);
  assert.deepEqual(good.policy, POLICY);

  // A key that does not hash to the pinned fingerprint is O16's refusal. What
  // stops a forged key is this check and not the QR: the QR is a string anybody
  // can print.
  const forged = parseRestoreResponse({
    status: 200,
    body: sessionAnswer({ consumer_public_key: base64url(new Uint8Array(CONSUMER_KEY_BYTES).fill(9)) }),
    consumerKeyFingerprint: FINGERPRINT,
  });
  assert.equal(forged.code, RESTORE_CODES.CONSUMER_KEY_MISMATCH);
  assert.equal(forged.field, 'consumer_public_key');
  assert.equal(
    parseRestoreResponse({ status: 200, body: sessionAnswer(), consumerKeyFingerprint: 'A'.repeat(43) }).code,
    RESTORE_CODES.CONSUMER_KEY_MISMATCH,
  );
  // Nothing pinned is nothing to accept: an entry from before the fingerprint
  // existed restores no session at all.
  assert.equal(parseRestoreResponse({ status: 200, body: sessionAnswer() }).code, RESTORE_CODES.BAD_ENTRY);
  assert.equal(parseRestoreResponse({ status: 200, body: sessionAnswer(), consumerKeyFingerprint: '' }).code, RESTORE_CODES.BAD_ENTRY);
});

test('a restore answer missing a bootstrap value is refused, never half-read', () => {
  for (const [member, value] of [
    ['project_id', ''],
    ['project_id', 'has space'],
    ['endpoint', 'http://x.invalid'],
    ['database_url', ''],
    ['api_key', ''],
    ['role', ''],
    ['refresh_token', ''],
    ['custom_token', ''],
    ['expires_in', 0],
    ['consumer_public_key', 'not base64url'],
  ]) {
    const answer = parseRestoreResponse({ status: 200, body: sessionAnswer({ [member]: value }), consumerKeyFingerprint: FINGERPRINT });
    assert.equal(answer.ok, false, member);
    // A key that will not decode is refused by the fingerprint's own reader, so
    // its field is the key's and not the answer's.
    assert.equal(answer.field, member === 'consumer_public_key' ? 'consumer_public_key' : member, member);
  }
  assert.equal(parseRestoreResponse({ status: 200, body: '{}', consumerKeyFingerprint: FINGERPRINT }).field, 'consumer_public_key');
  assert.equal(parseRestoreResponse({ status: 200, body: 'not json', consumerKeyFingerprint: FINGERPRINT }).code, RESTORE_CODES.BAD_RESPONSE);
});

test('the project’s own refusals are carried through as themselves', () => {
  // The strict switch's answer is a project's decision, and a phone shows it as
  // "pair by QR" rather than as a failure of the restore (D2796).
  const strict = parseRestoreResponse({ status: 403, body: '{"code":"strict_new_device_needs_qr"}', consumerKeyFingerprint: FINGERPRINT });
  assert.equal(strict.code, 'strict_new_device_needs_qr');
  assert.equal(strict.ok, false);
  assert.equal(parseRestoreResponse({ status: 401, body: '{"code":"not_a_member"}', consumerKeyFingerprint: FINGERPRINT }).code, 'not_a_member');
  assert.equal(parseRestoreResponse({ status: 403, body: '{"code":"mobile_disabled"}', consumerKeyFingerprint: FINGERPRINT }).code, 'mobile_disabled');
  // A code C1 does not carry is not a screen, so it is never shown as one.
  assert.equal(parseRestoreResponse({ status: 500, body: '{"code":"who_knows"}', consumerKeyFingerprint: FINGERPRINT }).code, RESTORE_CODES.UNKNOWN_CODE);
  assert.equal(parseRestoreResponse({ status: 500, body: '', consumerKeyFingerprint: FINGERPRINT }).code, RESTORE_CODES.UNKNOWN_CODE);
});

test('a challenge is asked for with the empty body the contract declares', async () => {
  const { fetch, calls } = project();
  const challenged = await requestRestoreChallenge({ endpoint: ENDPOINT, fetch });
  assert.equal(challenged.ok, true);
  assert.equal(challenged.challenge, CHALLENGE);
  assert.equal(challenged.expiresIn, 600);
  assert.equal(calls[0].url, `${ENDPOINT}/mobile/v1/restore/challenge`);
  assert.equal(calls[0].method, 'POST');
  assert.equal(calls[0].body, '{}');

  assert.equal(parseChallengeResponse({ status: 200, body: '{"challenge":"","expires_in":600}' }).code, RESTORE_CODES.BAD_CHALLENGE);
  assert.equal(parseChallengeResponse({ status: 200, body: '{"challenge":"x"}' }).field, 'expires_in');
  assert.equal(parseChallengeResponse({ status: 200, body: '{"challenge":"x","expires_in":0}' }).code, RESTORE_CODES.BAD_RESPONSE);
  assert.equal(parseChallengeResponse({ status: 403, body: '{"code":"mobile_disabled"}' }).code, 'mobile_disabled');
  assert.equal(parseChallengeResponse({ status: 403, body: '{"code":"who_knows"}' }).code, RESTORE_CODES.UNKNOWN_CODE);
  // The challenge is a project's own route, so a cleartext scheme never leaves.
  assert.equal((await requestRestoreChallenge({ endpoint: 'http://x.invalid', fetch })).code, RESTORE_CODES.BAD_ENDPOINT);
  assert.equal((await requestRestoreChallenge({ endpoint: ENDPOINT })).code, RESTORE_CODES.BAD_CONFIG);
});

test('a restore restores one project and hands back C2’s port', async () => {
  const { fetch, calls } = project();
  const { assertionFor, asked } = assertionOf(ASSERTION);
  const { deviceKey, projectStore } = stores();
  const restored = await restoreProject({ entry: ENTRY, identityId: IDENTITY, assertionFor, deviceKey, projectStore, fetch });
  assert.equal(restored.ok, true);
  assert.equal(restored.databaseUrl, DATABASE_URL);
  assert.deepEqual(restored.entry, ENTRY);

  // The assertion is bound to the audience the project requires (F39) and to
  // the challenge this project issued a moment ago -- and to no other.
  assert.deepEqual(asked, [{ audience: ENDPOINT, challenge: CHALLENGE, identityId: IDENTITY }]);
  const [, restore] = calls;
  assert.equal(restore.url, `${ENDPOINT}/mobile/v1/restore`);
  assert.equal(restore.method, 'POST');
  // The device's own public key travels once, in the one spelling this tree
  // uses, and there is no token in the body: the key is what the answer's token
  // is bound to.
  assert.deepEqual(JSON.parse(restore.body), {
    assertion: ASSERTION,
    challenge: CHALLENGE,
    device_public_key: base64url(PUBLIC_KEY),
  });

  const session = assertSessionPort(restored.session);
  assert.equal(session.project_id, PROJECT);
  assert.equal(session.endpoint, ENDPOINT);
  assert.equal(session.role, 'operator');
  assert.deepEqual(session.policy, PORT_POLICY);
  assert.deepEqual(await session.signMessage(new Uint8Array([1, 2, 3])), new Uint8Array(64).fill(SEED[0]));
  // The token the session refreshes with is the one the answer carried, and it
  // is in the store the refresh rotates.
  assert.equal((await projectStore.get(PROJECT)).token, TOKEN);
});

test('a project that refuses takes the new device key back with it', async () => {
  const refused = [
    ['strict', { restore: { status: 403, body: '{"code":"strict_new_device_needs_qr"}' } }, 'strict_new_device_needs_qr'],
    ['forged', { restore: { status: 200, body: sessionAnswer({ consumer_public_key: base64url(new Uint8Array(32).fill(3)) }) } }, RESTORE_CODES.CONSUMER_KEY_MISMATCH],
    ['offline', { restore: { status: 500, body: 'not json' } }, RESTORE_CODES.UNKNOWN_CODE],
  ];
  for (const [name, options, code] of refused) {
    const { fetch } = project(options);
    const { assertionFor } = assertionOf(ASSERTION);
    const { deviceKey, projectStore } = stores();
    const restored = await restoreProject({ entry: ENTRY, identityId: IDENTITY, assertionFor, deviceKey, projectStore, fetch });
    assert.equal(restored.code, code, name);
    // A key left behind would refuse the next attempt `already-held` -- and a
    // person whose strict project refused them must still be able to pair by
    // QR, which needs a device key this phone does not yet hold.
    assert.equal((await deviceKey.publicKey(PROJECT)).code, 'absent', name);
    assert.equal((await projectStore.get(PROJECT)).code, 'absent', name);
  }
});

test('an assertion the identity project would not sign restores nothing', async () => {
  for (const answer of [null, '', undefined]) {
    const { fetch, calls } = project();
    const { assertionFor } = assertionOf(answer);
    const { deviceKey, projectStore } = stores();
    const restored = await restoreProject({ entry: ENTRY, identityId: IDENTITY, assertionFor, deviceKey, projectStore, fetch });
    assert.equal(restored.code, RESTORE_CODES.ASSERTION_ABSENT, String(answer));    // The challenge was asked for and the restore route was never posted to.
    assert.equal(calls.length, 1);
    assert.equal((await deviceKey.publicKey(PROJECT)).code, 'absent');
  }
  const thrown = await restoreProject({
    entry: ENTRY,
    identityId: IDENTITY,
    assertionFor: async () => {
      throw new Error('the identity project is unreachable');
    },
    ...stores(),
    fetch: project().fetch,
  });
  // What the identity client threw may quote a token, so it is not carried.
  assert.equal(thrown.code, RESTORE_CODES.ASSERTION_ABSENT);
  assert.equal(thrown.field, 'assertion');
});

test('an answer for another project is refused whatever its key hashes to', async () => {
  const { fetch } = project({ restore: { status: 200, body: sessionAnswer({ project_id: OTHER_PROJECT }) } });
  const { assertionFor } = assertionOf(ASSERTION);
  const { deviceKey, projectStore } = stores();
  const restored = await restoreProject({ entry: ENTRY, identityId: IDENTITY, assertionFor, deviceKey, projectStore, fetch });
  assert.equal(restored.code, RESTORE_CODES.PROJECT_MISMATCH);
  assert.equal((await deviceKey.publicKey(PROJECT)).code, 'absent');
  assert.equal((await projectStore.get(PROJECT)).code, 'absent');
});

test('a phone that is not new says so rather than minting a second key', async () => {
  const { fetch, calls } = project();
  const { assertionFor } = assertionOf(ASSERTION);
  const { deviceKey, projectStore } = stores();
  await deviceKey.create(PROJECT);
  const restored = await restoreProject({ entry: ENTRY, identityId: IDENTITY, assertionFor, deviceKey, projectStore, fetch });
  // `already-held` is the device key store's own code, carried through: it is
  // not a failure of the restore but the answer that this phone is paired.
  assert.equal(restored.code, 'already-held');
  assert.deepEqual(calls, []);
});

test('one project’s refusal does not hold up another', async () => {
  const { fetch } = project({ restore: { status: 403, body: '{"code":"strict_new_device_needs_qr"}' } });
  const { assertionFor } = assertionOf(ASSERTION);
  const { deviceKey, projectStore } = stores();
  const restored = await restoreAll({
    identityId: IDENTITY,
    entries: [ENTRY, OTHER_ENTRY],
    assertionFor: async ({ audience, challenge, identityId }) => {
      // Answer for the first project only, so the second is refused by its own
      // missing assertion rather than by anything the first answered.
      assert.equal(identityId, IDENTITY);
      assert.equal(challenge, CHALLENGE);
      return audience === ENDPOINT ? ASSERTION : null;
    },
    deviceKey,
    projectStore,
    fetch,
  });
  assert.equal(restored.ok, false);
  assert.deepEqual(restored.sessions, []);
  assert.deepEqual(restored.failed, [
    { project_id: PROJECT, code: 'strict_new_device_needs_qr' },
    { project_id: OTHER_PROJECT, code: RESTORE_CODES.ASSERTION_ABSENT },
  ]);
});

test('with the sync switch off the list comes from the phone and the identity project is not called', async () => {
  const { deviceKey, projectStore, list } = stores();
  await list.write(IDENTITY, [ENTRY], { synced: true });
  const { assertionFor } = assertionOf(ASSERTION);
  let reached = 0;
  const fetch = async (url) => {
    reached += 1;
    // The list is the phone's own item, so the only requests are the project's
    // own two routes -- and this task's red step is exactly that the identity
    // project is never one of them.
    assert.ok(url.startsWith(ENDPOINT), `the identity project must not be called while the project does not sync: ${url}`);
    if (url.endsWith('/mobile/v1/restore/challenge')) {
      return { status: 200, text: async () => JSON.stringify({ challenge: CHALLENGE, expires_in: 600 }) };
    }
    return { status: 200, text: async () => sessionAnswer() };
  };
  const restored = await restoreIdentity({
    identityId: IDENTITY,
    sync: false,
    identityEndpoint: IDENTITY_ENDPOINT,
    idToken: ID_TOKEN,
    store: list,
    assertionFor,
    deviceKey,
    projectStore,
    fetch,
  });
  assert.equal(restored.ok, true);
  assert.equal(restored.stored, true);
  assert.equal(reached, 2);
  assert.equal(restored.sessions.length, 1);
  assert.equal(restored.sessions[0].project_id, PROJECT);
  assert.deepEqual(restored.failed, []);
  // The list is written back at the level the switch names, which is what makes
  // it the copy a new device of the same person finds in the platform's backup.
  assert.equal((await list.read(IDENTITY)).entries.length, 1);
});

test('with the sync switch on the list comes from the identity project and keeps the names the phone had', async () => {
  const { deviceKey, projectStore, list } = stores();
  await list.write(IDENTITY, [ENTRY]);
  const { assertionFor } = assertionOf(ASSERTION);
  const calls = [];
  const fetch = async (url, options = {}) => {
    calls.push({ url, method: options.method, headers: options.headers, body: options.body });
    if (url === `${IDENTITY_ENDPOINT}/paired-list`) {
      // The identity project holds one entry, and it holds no name: a name is
      // the phone's own field and is not on the wire (D2796).
      const { name, ...wire } = ENTRY;
      return { status: 200, text: async () => JSON.stringify({ entries: [wire] }) };
    }
    if (url.endsWith('/mobile/v1/restore/challenge')) {
      return { status: 200, text: async () => JSON.stringify({ challenge: CHALLENGE, expires_in: 600 }) };
    }
    return { status: 200, text: async () => sessionAnswer() };
  };
  const restored = await restoreIdentity({
    identityId: IDENTITY,
    sync: true,
    identityEndpoint: IDENTITY_ENDPOINT,
    idToken: ID_TOKEN,
    store: list,
    assertionFor,
    deviceKey,
    projectStore,
    fetch,
  });
  assert.equal(restored.ok, true);
  assert.equal(calls[0].url, `${IDENTITY_ENDPOINT}/paired-list`);
  assert.equal(calls[0].method, 'GET');
  assert.deepEqual(calls[0].headers, { Authorization: `Bearer ${ID_TOKEN}` });
  assert.equal(restored.sessions.length, 1);
  // The readable name is the phone's own and survives a list that came back
  // without it, and the phone's item is written device-only: with the switch on
  // the copy that has to outlive the phone is the identity project's.
  assert.deepEqual((await list.read(IDENTITY)).entries, [ENTRY]);
});

test('a list the identity project could not be asked for restores nothing', async () => {
  const { deviceKey, projectStore, list } = stores();
  await list.write(IDENTITY, [ENTRY]);
  const { assertionFor } = assertionOf(ASSERTION);
  const restored = await restoreIdentity({
    identityId: IDENTITY,
    sync: true,
    identityEndpoint: IDENTITY_ENDPOINT,
    idToken: ID_TOKEN,
    store: list,
    assertionFor,
    deviceKey,
    projectStore,
    fetch: async () => ({ status: 503, text: async () => '{"error":{"code":"paired_list_unavailable"}}' }),
  });
  assert.equal(restored.code, 'paired_list_unavailable');
});

test('a switch that is not a boolean restores from nowhere', async () => {
  const { list } = stores();
  const reached = async () => assert.fail('nothing may be read before the switch is known');
  for (const sync of [undefined, null, 'on', 1]) {
    const restored = await restoreIdentity({ identityId: IDENTITY, sync, store: list, fetch: reached });
    assert.equal(restored.code, RESTORE_CODES.BAD_SWITCH, String(sync));
    assert.equal(restored.field, 'sync');
  }
  // And the wiring is checked before any project is reached either.
  assert.equal((await restoreIdentity({ identityId: IDENTITY, sync: false })).code, RESTORE_CODES.BAD_CONFIG);
});
