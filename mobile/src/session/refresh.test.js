// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The hourly refresh, the custom-token exchange and the rotation (task M3;
// D2783, D2794).
//
// The red half of M3 is here: a refresh whose token another device rotated out
// answers `device_revoked`, and the session must fail and sign the project out
// first -- never hand back a token it no longer has.

import assert from 'node:assert/strict';
import test from 'node:test';

import { MemorySecureStore } from '../accounts/secure-store.js';

import {
  CUSTOM_TOKEN_ENDPOINT,
  ProjectSessionStore,
  REFRESH_CODES,
  REFRESH_SKEW_MS,
  buildCustomTokenExchange,
  buildRefreshRequest,
  buildRevokeSelfRequest,
  parseCustomTokenExchange,
  parseRefreshResponse,
  projectSessionItem,
  refreshSession,
  sessionPolicyOf,
} from './refresh.js';

const ENDPOINT = 'https://mobile-example.a.run.app';
const TOKEN = 'not-a-credential-test-refresh-token';
const SIGNATURE = 'A'.repeat(86);

/** The refresh answer C1's RefreshResponse names, as the service serialises it. */
function answer(overrides = {}) {
  return JSON.stringify({
    refresh_token: 'not-a-credential-test-next-refresh-token',
    custom_token: 'not-a-credential-test-custom-token',
    expires_in: 3600,
    role: 'operator',
    policy: {
      costly_modes: ['pro', 'critical'],
      biometric_on_costly: false,
      compact_threshold_percent: 40,
      crash_reports: true,
    },
    ...overrides,
  });
}

test('the refresh request is the token and the device key’s signature over it', () => {
  const built = buildRefreshRequest({ endpoint: ENDPOINT, refreshToken: TOKEN, signature: SIGNATURE });
  assert.equal(built.ok, true);
  assert.equal(built.request.url, `${ENDPOINT}/mobile/v1/refresh`);
  assert.equal(built.request.method, 'POST');
  assert.deepEqual(JSON.parse(built.request.body), { refresh_token: TOKEN, signature: SIGNATURE });
  // The endpoint is where a device-bound token is presented, so a cleartext
  // scheme is refused before the token leaves.
  assert.equal(buildRefreshRequest({ endpoint: 'http://x.invalid', refreshToken: TOKEN, signature: SIGNATURE }).code, REFRESH_CODES.BAD_ENDPOINT);
  assert.equal(buildRefreshRequest({ endpoint: ENDPOINT, refreshToken: '', signature: SIGNATURE }).code, REFRESH_CODES.BAD_TOKEN);
  assert.equal(buildRefreshRequest({ endpoint: ENDPOINT, refreshToken: TOKEN, signature: '' }).code, REFRESH_CODES.BAD_SIGNATURE);
});

test('the revoke-self request is the same shape as the refresh', () => {
  const built = buildRevokeSelfRequest({ endpoint: ENDPOINT, refreshToken: TOKEN, signature: SIGNATURE });
  assert.equal(built.ok, true);
  assert.equal(built.request.url, `${ENDPOINT}/mobile/v1/revoke-self`);
  assert.deepEqual(JSON.parse(built.request.body), { refresh_token: TOKEN, signature: SIGNATURE });
});

test('a refresh answer is read as the successor token, the custom token and the policy', () => {
  const parsed = parseRefreshResponse({ status: 200, body: answer() });
  assert.equal(parsed.ok, true);
  assert.equal(parsed.refreshToken, 'not-a-credential-test-next-refresh-token');
  assert.equal(parsed.customToken, 'not-a-credential-test-custom-token');
  assert.equal(parsed.expiresIn, 3600);
  assert.equal(parsed.role, 'operator');
  // The Go shape's percent is the port's fraction, and 0 is the port's null.
  assert.deepEqual(parsed.policy, {
    costly_modes: ['pro', 'critical'],
    biometric_on_costly: false,
    compaction_threshold: 0.4,
    crash_reports: true,
  });
  assert.equal(sessionPolicyOf({ compact_threshold_percent: 0 }).compaction_threshold, null);
});

test('a refusal is the contract’s code, and no value travels in it', () => {
  const refused = parseRefreshResponse({ status: 403, body: JSON.stringify({ code: 'device_revoked', message: 'gone' }) });
  assert.equal(refused.ok, false);
  assert.equal(refused.code, REFRESH_CODES.DEVICE_REVOKED);
  assert.equal(JSON.stringify(refused).includes(TOKEN), false);
  // A code the contract does not carry is refused outright rather than shown.
  const unknown = parseRefreshResponse({ status: 403, body: JSON.stringify({ code: 'who_knows' }) });
  assert.equal(unknown.code, REFRESH_CODES.UNKNOWN_CODE);
  // A 200 whose body is not the shape is a bad response, not a session.
  assert.equal(parseRefreshResponse({ status: 200, body: '{}' }).code, REFRESH_CODES.BAD_RESPONSE);
});

test('the custom token is exchanged for an ID token, and Firebase’s refresh token is dropped', () => {
  const built = buildCustomTokenExchange({ apiKey: 'not-a-credential-test-api-key', customToken: 'not-a-credential-test-custom-token' });
  assert.equal(built.ok, true);
  assert.ok(built.request.url.startsWith(`${CUSTOM_TOKEN_ENDPOINT}?key=`));
  const parsed = parseCustomTokenExchange({
    status: 200,
    body: JSON.stringify({ idToken: 'not-a-credential-test-id-token', refreshToken: 'not-a-credential-test-firebase-refresh', expiresIn: '3600' }),
  });
  assert.equal(parsed.ok, true);
  assert.equal(parsed.idToken, 'not-a-credential-test-id-token');
  assert.equal(parsed.expiresIn, 3600);
  // D2783: the app never keeps the Firebase refresh token, so it is not here.
  assert.equal('refreshToken' in parsed, false);
});

test('the project session item is the contract’s name under the project id', () => {
  assert.equal(projectSessionItem('fugaro-example'), 'fugaro.project-session.fugaro-example');
  assert.throws(() => projectSessionItem('has space'), TypeError);
});

test('the project session store keeps one rotating token per project', async () => {
  const port = new MemorySecureStore();
  const store = new ProjectSessionStore(port);
  assert.equal((await store.get('fugaro-example')).code, REFRESH_CODES.ABSENT);
  assert.equal((await store.put('fugaro-example', TOKEN)).ok, true);
  assert.deepEqual(port.keys(), ['fugaro.project-session.fugaro-example']);
  assert.equal((await store.get('fugaro-example')).token, TOKEN);
  assert.equal((await store.delete('fugaro-example')).ok, true);
  assert.equal((await store.get('fugaro-example')).code, REFRESH_CODES.ABSENT);
});

test('a refresh that the project refuses as revoked signs the project out', async () => {
  const seen = [];
  const fetchPort = async (url, options) => {
    seen.push(JSON.parse(options.body));
    return { status: 403, text: async () => JSON.stringify({ code: 'device_revoked' }) };
  };
  const result = await refreshSession({
    endpoint: ENDPOINT,
    refreshToken: TOKEN,
    sign: async () => new Uint8Array(64).fill(1),
    apiKey: 'not-a-credential-test-api-key',
    fetch: fetchPort,
  });
  assert.equal(result.ok, false);
  assert.equal(result.code, REFRESH_CODES.DEVICE_REVOKED);
  assert.equal(result.revoked, true);
  // The request carried the signature over the token and nothing else.
  assert.deepEqual(Object.keys(seen[0]).sort(), ['refresh_token', 'signature']);
});

test('a rotation whose exchange failed still hands back the successor', async () => {
  const fetchPort = async (url) => {
    if (url.includes('/mobile/v1/refresh')) return { status: 200, text: async () => answer() };
    return { status: 500, text: async () => JSON.stringify({ code: 'device_revoked' }) };
  };
  const result = await refreshSession({
    endpoint: ENDPOINT,
    refreshToken: TOKEN,
    sign: async () => new Uint8Array(64).fill(1),
    apiKey: 'not-a-credential-test-api-key',
    fetch: fetchPort,
  });
  assert.equal(result.ok, false);
  // The rotation already happened, so the successor is the live token and the
  // caller must write it: presenting the presented one again is a replay the
  // service answers `device_revoked`.
  assert.equal(result.refreshToken, 'not-a-credential-test-next-refresh-token');
});

test('a rotation hands back the successor and the exchanged ID token', async () => {
  const fetchPort = async (url) => {
    if (url.includes('/mobile/v1/refresh')) return { status: 200, text: async () => answer() };
    return {
      status: 200,
      text: async () => JSON.stringify({ idToken: 'not-a-credential-test-id-token', refreshToken: 'not-a-credential-test-firebase-refresh', expiresIn: '3600' }),
    };
  };
  const result = await refreshSession({
    endpoint: ENDPOINT,
    refreshToken: TOKEN,
    sign: async () => new Uint8Array(64).fill(1),
    apiKey: 'not-a-credential-test-api-key',
    fetch: fetchPort,
  });
  assert.equal(result.ok, true);
  assert.equal(result.refreshToken, 'not-a-credential-test-next-refresh-token');
  assert.equal(result.idToken, 'not-a-credential-test-id-token');
  assert.equal(result.expiresIn, 3600);
  assert.equal(REFRESH_SKEW_MS > 0, true);
});
