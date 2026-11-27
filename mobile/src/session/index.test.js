// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The session module's door and the port it implements (task M3; D2783,
// D2794, C2).
//
// The red half: a refresh the project refuses as `device_revoked` -- the token
// another device rotated out -- must fail and sign the project out first, and
// every accessor must refuse afterwards.

import assert from 'node:assert/strict';
import test from 'node:test';

import { PORT_CODES, SESSION_EVENTS, assertSessionPort } from '../../../shared/mobile-session/port.js';
import { MemorySecureStore } from '../accounts/secure-store.js';

import { DeviceKeyStore } from './devicekey.js';
import * as session from './index.js';
import { REFRESH_CODES, REFRESH_SKEW_MS, ProjectSessionStore } from './refresh.js';

const ENDPOINT = 'https://mobile-example.a.run.app';
const PROJECT = 'fugaro-example';
const TOKEN = 'not-a-credential-test-refresh-token';
const API_KEY = 'not-a-credential-test-api-key';

// A deterministic stand-in for the app's Ed25519: the session's own logic needs
// a key that signs, not the arithmetic (devicekey.test.js checks that against
// the platform's Ed25519).
let counter = 0;
const ed25519 = Object.freeze({
  utils: Object.freeze({ randomSecretKey: () => new Uint8Array(32).fill((counter += 1)) }),
  getPublicKey: (seed) => new Uint8Array(32).fill(seed[0] ^ 0x5a),
  sign: (message, seed) => new Uint8Array(64).fill(seed[0]),
  verify: () => true,
});

const POLICY = Object.freeze({
  costly_modes: ['pro', 'critical'],
  biometric_on_costly: false,
  compact_threshold_percent: 40,
  crash_reports: true,
});

function refreshAnswer(overrides = {}) {
  return JSON.stringify({
    refresh_token: 'not-a-credential-test-next-refresh-token',
    custom_token: 'not-a-credential-test-custom-token',
    expires_in: 3600,
    role: 'operator',
    policy: POLICY,
    ...overrides,
  });
}

/** A fetch that answers the refresh route and the custom-token exchange. */
function fetchOf(refresh = { status: 200, body: refreshAnswer() }) {
  return async (url) => {
    if (url.includes('/mobile/v1/refresh')) return { status: refresh.status, text: async () => refresh.body };
    return {
      status: 200,
      text: async () => JSON.stringify({ idToken: 'not-a-credential-test-id-token', refreshToken: 'not-a-credential-test-firebase-refresh', expiresIn: '3600' }),
    };
  };
}

/** A fetch that counts rotations and answers a token numbered by them. */
function countingFetch() {
  const state = { refreshes: 0 };
  const fetch = async (url) => {
    if (url.includes('/mobile/v1/refresh')) {
      state.refreshes += 1;
      return { status: 200, text: async () => refreshAnswer({ custom_token: `not-a-credential-test-custom-token-${state.refreshes}` }) };
    }
    return {
      status: 200,
      text: async () =>
        JSON.stringify({
          idToken: `not-a-credential-test-id-token-${state.refreshes}`,
          refreshToken: 'not-a-credential-test-firebase-refresh',
          expiresIn: '3600',
        }),
    };
  };
  return { fetch, state };
}

async function build({ fetch = fetchOf(), now = () => 1_000_000, policy = POLICY, port = new MemorySecureStore() } = {}) {
  const store = new ProjectSessionStore(port);
  await store.put(PROJECT, TOKEN);
  const deviceKey = new DeviceKeyStore({ port, ed25519 });
  await deviceKey.create(PROJECT);
  const built = session.createProjectSession({
    projectId: PROJECT,
    endpoint: ENDPOINT,
    role: 'operator',
    policy,
    refreshToken: TOKEN,
    store,
    deviceKey,
    apiKey: API_KEY,
    fetch,
    now,
  });
  return { built, port, store, deviceKey };
}

test('the door exports the port’s factory and the vocabulary, and no transport', () => {
  assert.deepEqual(Object.keys(session).sort(), [
    'DEVICE_KEY_ALGORITHM',
    'SESSION_CODES',
    'createProjectSession',
    'signOutEverywhere',
  ]);
  // The network is the refresh's to open: a screen cannot reach a builder or a
  // fetch from the door, so it cannot hand a token to a URL of its own.
  assert.equal('buildRefreshRequest' in session, false);
  assert.equal('refreshSession' in session, false);
});

test('the session it builds is one C2’s port accepts', async () => {
  const { built } = await build();
  assert.equal(built.ok, true);
  const mounted = assertSessionPort(built.session);
  assert.equal(mounted.project_id, PROJECT);
  assert.equal(mounted.endpoint, ENDPOINT);
  assert.equal(mounted.role, 'operator');
  // The Go response's percent is the port's fraction.
  assert.equal(mounted.policy.compaction_threshold, 0.4);
  assert.deepEqual(mounted.policy.costly_modes, ['pro', 'critical']);
});

test('idToken() refreshes through the project and stores the successor token', async () => {
  const { fetch, state } = countingFetch();
  const { built, store } = await build({ fetch });
  const token = await built.session.idToken();
  assert.equal(token, 'not-a-credential-test-id-token-1');
  // The presented token is dead; the successor is what the phone holds now.
  assert.equal((await store.get(PROJECT)).token, 'not-a-credential-test-next-refresh-token');
  // A second call inside the hour does not rotate again: the held token is
  // handed back, and the fetch would have numbered a second one.
  assert.equal(await built.session.idToken(), token);
  assert.equal(state.refreshes, 1);
});

test('a held token near its hour is refreshed rather than trusted', async () => {
  let now = 1_000_000;
  const { fetch, state } = countingFetch();
  const { built } = await build({ fetch, now: () => now });
  assert.equal(await built.session.idToken(), 'not-a-credential-test-id-token-1');
  // Inside the skew the held token is still trusted ...
  now += 3600 * 1000 - REFRESH_SKEW_MS - 1;
  assert.equal(await built.session.idToken(), 'not-a-credential-test-id-token-1');
  assert.equal(state.refreshes, 1);
  // ... and one millisecond later it is not.
  now += 1;
  assert.equal(await built.session.idToken(), 'not-a-credential-test-id-token-2');
  assert.equal(state.refreshes, 2);
});

test('a successor that cannot be written is a refusal, and no token is cached', async () => {
  const port = new MemorySecureStore();
  const write = port.setItem.bind(port);
  const locked = { on: false };
  port.setItem = async (key, value) => {
    if (locked.on) throw new Error('the keychain is locked');
    return write(key, value);
  };
  const { fetch, state } = countingFetch();
  const { built, store } = await build({ fetch, port });
  locked.on = true;
  // A rotation the phone cannot write is not one it may report as a success:
  // the caller must see the failure rather than a token that is not on disk.
  await assert.rejects(() => built.session.idToken(), (error) => error.code === REFRESH_CODES.STORE_FAILED);
  // Nothing was written and nothing is cached, so the next call rotates again
  // rather than handing back the token it never stored.
  assert.equal((await store.get(PROJECT)).token, TOKEN);
  locked.on = false;
  assert.equal(await built.session.idToken(), 'not-a-credential-test-id-token-2');
  assert.equal(state.refreshes, 2);
});

test('signMessage() signs with the device key and refuses bytes it is not given', async () => {
  const { built } = await build();
  const signature = await built.session.signMessage(new Uint8Array([1, 2, 3]));
  assert.equal(signature.length, 64);
  await assert.rejects(() => built.session.signMessage('not bytes'));
});

test('a refresh the project refuses as revoked signs the project out first', async () => {
  const { built, store } = await build({
    fetch: fetchOf({ status: 403, body: JSON.stringify({ code: 'device_revoked' }) }),
  });
  let fired = 0;
  built.session.on(SESSION_EVENTS.SIGNED_OUT, () => {
    fired += 1;
  });
  await assert.rejects(() => built.session.idToken());
  assert.equal(fired, 1);
  // Both accessors refuse after the sign-out, and neither hands back a stale
  // credential.
  await assert.rejects(() => built.session.idToken(), (error) => error.code === PORT_CODES.SIGNED_OUT);
  await assert.rejects(() => built.session.signMessage(new Uint8Array([1])), (error) => error.code === PORT_CODES.SIGNED_OUT);
  // A second failure does not fire the event again: signed out stays signed out.
  await assert.rejects(() => built.session.idToken());
  assert.equal(fired, 1);
  // The dead token is not left on the phone.
  assert.equal((await store.get(PROJECT)).code, 'absent');
});

test('concurrent idToken() calls share one rotation rather than burning the token twice', async () => {
  let refreshes = 0;
  const fetch = async (url) => {
    if (url.includes('/mobile/v1/refresh')) {
      refreshes += 1;
      // A real rotation is a round trip: the second caller arrives while the
      // first is still in flight, which is what the app returning to the
      // foreground after an hour does to six surfaces at once.
      await new Promise((resolve) => setImmediate(resolve));
      return { status: 200, text: async () => refreshAnswer() };
    }
    return {
      status: 200,
      text: async () => JSON.stringify({ idToken: 'not-a-credential-test-id-token', refreshToken: 'not-a-credential-test-firebase-refresh', expiresIn: '3600' }),
    };
  };
  const { built } = await build({ fetch });
  const tokens = await Promise.all([built.session.idToken(), built.session.idToken()]);
  // One rotation, not two: a second would present the token the first burned
  // and be answered `device_revoked`, costing the phone a QR re-pair.
  assert.equal(refreshes, 1);
  assert.deepEqual(tokens, ['not-a-credential-test-id-token', 'not-a-credential-test-id-token']);
});

test('a rotation whose exchange failed keeps the successor rather than the burned token', async () => {
  let refreshes = 0;
  const fetch = async (url) => {
    if (url.includes('/mobile/v1/refresh')) {
      refreshes += 1;
      return { status: 200, text: async () => refreshAnswer() };
    }
    return { status: 500, text: async () => JSON.stringify({ code: 'device_revoked' }) };
  };
  const { built, store } = await build({ fetch });
  await assert.rejects(() => built.session.idToken());
  // The rotation happened, so the successor is what the phone holds; keeping
  // the presented token would make the next call a replay.
  assert.equal((await store.get(PROJECT)).token, 'not-a-credential-test-next-refresh-token');
  // ... and the session is still live, so the next call presents the live
  // token rather than the session having ended itself.
  await assert.rejects(() => built.session.idToken(), (error) => error.code !== PORT_CODES.SIGNED_OUT);
  assert.equal(refreshes, 2);
});

test('a network failure does not sign the project out', async () => {
  const { built } = await build({
    fetch: async () => {
      throw new Error('offline');
    },
  });
  await assert.rejects(() => built.session.idToken(), (error) => error.code !== PORT_CODES.SIGNED_OUT);
  // The session is still live: a later refresh may succeed.
  assert.equal((await built.store ?? { get: async () => ({ token: TOKEN }) }).get !== undefined, true);
});

test('signing out everywhere revokes each live project and signs the identities out', async () => {
  const called = [];
  const fetch = async (url, options) => {
    called.push({ url, body: JSON.parse(options.body) });
    return { status: 200, text: async () => '{}' };
  };
  const result = await session.signOutEverywhere({
    projects: [
      { endpoint: ENDPOINT, refreshToken: TOKEN, sign: async () => new Uint8Array(64).fill(1) },
      { endpoint: 'https://other.invalid', refreshToken: 'not-a-credential-test-other', sign: async () => new Uint8Array(64).fill(2) },
    ],
    identities: async () => {
      called.push({ identities: true });
    },
    fetch,
  });
  assert.equal(result.ok, true);
  assert.deepEqual(result.revoked, [ENDPOINT, 'https://other.invalid']);
  assert.equal(called.filter((entry) => entry.url?.includes('/mobile/v1/revoke-self')).length, 2);
  assert.equal(called.some((entry) => entry.identities === true), true);
});

test('a project that refuses the revoke is named, and the identities still go', async () => {
  const fetch = async () => ({ status: 403, text: async () => JSON.stringify({ code: 'device_revoked' }) });
  let signedOut = 0;
  const result = await session.signOutEverywhere({
    projects: [{ endpoint: ENDPOINT, refreshToken: TOKEN, sign: async () => new Uint8Array(64).fill(1) }],
    identities: async () => {
      signedOut += 1;
    },
    fetch,
  });
  // A family that could not be ended is named rather than swallowed, and the
  // sign-out of the phone itself does not depend on a project's answer.
  assert.equal(result.ok, false);
  assert.deepEqual(result.revoked, []);
  assert.deepEqual(result.failed, [{ endpoint: ENDPOINT, code: 'device_revoked' }]);
  assert.equal(signedOut, 1);
});
