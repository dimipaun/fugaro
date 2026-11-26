// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The identity store's tests (task M1; D2790, D2793, D2785).
//
// The sign-in and refresh exchanges are fakes handed in, so no network and no
// Keychain is needed: what is pinned here is the store's own rules -- several
// identities held independently, one secure-store item per identity, the relay
// refusal, a re-authentication that must land on the same person, and a cache
// that stays coherent with the store.

import assert from 'node:assert/strict';
import test from 'node:test';

import { MemorySecureStore, MemorySettingsStore } from '../accounts/index.js';
import { IdentitySessionStore } from '../accounts/secure-store.js';
import { IDENTITY_CODES, IdentityStore } from './store.js';
import { PROVIDER_CODES } from './providers.js';

const GOOGLE = {
  provider: 'google.com',
  sub: 'synthetic-google-sub',
  email: 'one@example.test',
  emailVerified: true,
  authTime: 1764000000,
};
const APPLE = {
  provider: 'apple.com',
  sub: 'synthetic.apple.sub',
  email: 'two@example.test',
  emailVerified: true,
  authTime: 1764000100,
};
const RELAY = {
  provider: 'apple.com',
  sub: 'synthetic.apple.relay',
  email: 'hidden@privaterelay.appleid.com',
  emailVerified: true,
  authTime: 1764000200,
};

/** A store over memory ports, with the exchanges faked from a table. */
function makeStore({ signInTable = {}, refreshTable = {}, sessions } = {}) {
  const settings = new MemorySettingsStore();
  const secrets = new MemorySecureStore();
  const store = new IdentityStore({
    settings,
    sessions: sessions ?? new IdentitySessionStore(secrets),
    apiKey: 'synthetic-api-key',
    signIn: async ({ provider, idToken }) => {
      const session = signInTable[idToken];
      if (session === undefined || session.provider !== provider) {
        return Object.freeze({ ok: false, code: PROVIDER_CODES.SIGN_IN_REFUSED, field: 'idToken' });
      }
      return Object.freeze({ ok: true, session });
    },
    refresh: async ({ refreshToken }) => {
      const tokens = refreshTable[refreshToken];
      if (tokens === undefined) {
        return Object.freeze({ ok: false, code: PROVIDER_CODES.SIGN_IN_REFUSED, field: 'refreshToken', reason: 'TOKEN_EXPIRED' });
      }
      return Object.freeze({ ok: true, tokens });
    },
  });
  return { store, settings, secrets };
}

/** A provider session as providers.js parses it, for the sign-in table. */
function sessionOf(
  identity,
  { idToken = `synthetic-id-token-${identity.sub}`, refreshToken = `synthetic-refresh-${identity.sub}`, expiresIn = 3600 } = {},
) {
  return { ...identity, idToken, refreshToken, expiresIn };
}

/** The identity id the store files `identity` under. */
function idOf(identity) {
  return `${identity.provider}.${identity.sub}`;
}

test('several identities are held, each with email, provider, sub and the last authentication time', async () => {
  const { store } = makeStore({ signInTable: { 'token-g': sessionOf(GOOGLE), 'token-a': sessionOf(APPLE) } });
  await store.add({ provider: 'google.com', idToken: 'token-g' });
  await store.add({ provider: 'apple.com', idToken: 'token-a' });
  const listed = await store.list();
  assert.equal(listed.ok, true);
  assert.deepEqual(
    listed.identities.map((i) => i.id).sort(),
    [idOf(GOOGLE), idOf(APPLE)].sort(),
  );
  const google = listed.identities.find((i) => i.id === idOf(GOOGLE));
  assert.equal(google.provider, 'google.com');
  assert.equal(google.sub, GOOGLE.sub);
  assert.equal(google.email, GOOGLE.email);
  assert.equal(google.authTime, GOOGLE.authTime);
  assert.equal(google.relay, false);
  assert.equal(google.usableForPairing, true);
});

test('an Apple private-relay identity is flagged, and refused for pairing (the red step)', async () => {
  const { store } = makeStore({ signInTable: { 'token-r': sessionOf(RELAY) } });
  await store.add({ provider: 'apple.com', idToken: 'token-r' });
  const listed = await store.list();
  const relay = listed.identities.find((i) => i.id === idOf(RELAY));
  assert.equal(relay.relay, true, 'the flag is on the record a screen lists');
  assert.equal(relay.usableForPairing, false);

  const refused = await store.forPairing(idOf(RELAY));
  assert.equal(refused.ok, false);
  assert.equal(refused.code, IDENTITY_CODES.RELAY_ADDRESS);
  assert.equal(refused.field, 'email');
  // A real address under the same provider pairs fine.
  const { store: second } = makeStore({ signInTable: { 'token-a': sessionOf(APPLE) } });
  await second.add({ provider: 'apple.com', idToken: 'token-a' });
  const usable = await second.forPairing(idOf(APPLE));
  assert.equal(usable.ok, true);
  assert.equal(usable.identity.email, APPLE.email);
});

test('an identity with no email at all is unusable for pairing, with its own code', async () => {
  const hidden = { ...APPLE, sub: 'synthetic.apple.hidden', email: null };
  const { store } = makeStore({ signInTable: { 'token-h': sessionOf(hidden) } });
  await store.add({ provider: 'apple.com', idToken: 'token-h' });
  const listed = await store.list();
  assert.equal(listed.identities[0].usableForPairing, false);
  const refused = await store.forPairing(idOf(hidden));
  assert.equal(refused.code, IDENTITY_CODES.NO_EMAIL);
});

test('one secure-store item per identity, and removing one leaves the other whole', async () => {
  const { store, secrets } = makeStore({
    signInTable: { 'token-g': sessionOf(GOOGLE), 'token-a': sessionOf(APPLE) },
    refreshTable: { 'synthetic-refresh-google-sub': { idToken: 'synthetic-fresh-google', refreshToken: 'synthetic-refresh-google-sub', expiresIn: 3600 } },
  });
  await store.add({ provider: 'google.com', idToken: 'token-g' });
  await store.add({ provider: 'apple.com', idToken: 'token-a' });
  assert.deepEqual(secrets.keys(), [`fugaro.identity-session.${idOf(GOOGLE)}`, `fugaro.identity-session.${idOf(APPLE)}`].sort());

  const removed = await store.remove(idOf(GOOGLE));
  assert.equal(removed.ok, true);
  assert.deepEqual(secrets.keys(), [`fugaro.identity-session.${idOf(APPLE)}`]);
  const listed = await store.list();
  assert.deepEqual(listed.identities.map((i) => i.id), [idOf(APPLE)]);

  // The removed identity is gone from every door...
  assert.equal((await store.forPairing(idOf(GOOGLE))).code, IDENTITY_CODES.UNKNOWN_IDENTITY);
  assert.equal((await store.idToken(idOf(GOOGLE))).code, IDENTITY_CODES.UNKNOWN_IDENTITY);
  // ... and the other identity still answers on its own.
  const fresh = await store.idToken(idOf(APPLE));
  assert.equal(fresh.ok, true);
});

test('a duplicate identity is refused, and a failed sign-in adds nothing', async () => {
  const { store, secrets } = makeStore({ signInTable: { 'token-g': sessionOf(GOOGLE) } });
  await store.add({ provider: 'google.com', idToken: 'token-g' });
  const again = await store.add({ provider: 'google.com', idToken: 'token-g' });
  assert.equal(again.code, IDENTITY_CODES.DUPLICATE_IDENTITY);

  const refused = await store.add({ provider: 'google.com', idToken: 'token-unknown' });
  assert.equal(refused.code, PROVIDER_CODES.SIGN_IN_REFUSED, 'the exchange\'s refusal is the store\'s, unchanged');
  assert.equal((await store.list()).identities.length, 1);
  assert.equal(secrets.keys().length, 1);
});

test('re-authentication updates the authentication time and the session, independently per identity', async () => {
  const older = { ...GOOGLE, authTime: 1763000000 };
  const { store, secrets } = makeStore({
    signInTable: {
      'token-old': sessionOf(older, { refreshToken: 'synthetic-refresh-old' }),
      'token-new': sessionOf(GOOGLE, { refreshToken: 'synthetic-refresh-new' }),
      'token-a': sessionOf(APPLE),
    },
  });
  await store.add({ provider: 'google.com', idToken: 'token-old' });
  await store.add({ provider: 'apple.com', idToken: 'token-a' });

  const reauthed = await store.reauthenticate(idOf(older), { idToken: 'token-new' });
  assert.equal(reauthed.ok, true);
  const listed = await store.list();
  const google = listed.identities.find((i) => i.id === idOf(GOOGLE));
  assert.equal(google.authTime, GOOGLE.authTime, 'the new sign-in\'s time replaces the old');
  assert.equal(await secrets.getItem(`fugaro.identity-session.${idOf(GOOGLE)}`), 'synthetic-refresh-new');
  // The other identity was not touched.
  assert.equal(listed.identities.find((i) => i.id === idOf(APPLE)).authTime, APPLE.authTime);
  assert.equal(await secrets.getItem(`fugaro.identity-session.${idOf(APPLE)}`), 'synthetic-refresh-apple-sub');
});

test('a re-authentication that lands on another person is refused and changes nothing', async () => {
  const someoneElse = { ...GOOGLE, sub: 'synthetic-other-sub' };
  const { store, secrets } = makeStore({
    signInTable: { 'token-g': sessionOf(GOOGLE), 'token-x': sessionOf(someoneElse) },
  });
  await store.add({ provider: 'google.com', idToken: 'token-g' });
  const refused = await store.reauthenticate(idOf(GOOGLE), { idToken: 'token-x' });
  assert.equal(refused.ok, false);
  assert.equal(refused.code, IDENTITY_CODES.IDENTITY_CHANGED);
  assert.equal(refused.field, 'sub');
  // The record and the session are exactly as they were.
  const listed = await store.list();
  assert.equal(listed.identities.length, 1);
  assert.equal(listed.identities[0].sub, GOOGLE.sub);
  assert.equal(await secrets.getItem(`fugaro.identity-session.${idOf(GOOGLE)}`), 'synthetic-refresh-google-sub');
});

test('the id token is refreshed through the stored session, cached within its life, and rotated on the store', async () => {
  let calls = 0;
  const secrets = new MemorySecureStore();
  const counting = new IdentityStore({
    settings: new MemorySettingsStore(),
    sessions: new IdentitySessionStore(secrets),
    apiKey: 'synthetic-api-key',
    // The sign-in's own token is already spent (expiresIn 0), so the first
    // idToken() is the refresh path; the refresh's answer lives an hour.
    signIn: async () => Object.freeze({ ok: true, session: sessionOf(GOOGLE, { expiresIn: 0 }) }),
    refresh: async () => {
      calls += 1;
      return Object.freeze({ ok: true, tokens: { idToken: `synthetic-fresh-${calls}`, refreshToken: `synthetic-rotated-${calls}`, expiresIn: 3600 } });
    },
  });
  await counting.add({ provider: 'google.com', idToken: 'token-g' });
  const first = await counting.idToken(idOf(GOOGLE));
  assert.equal(first.idToken, 'synthetic-fresh-1');
  assert.equal(calls, 1);
  const second = await counting.idToken(idOf(GOOGLE));
  assert.equal(second.idToken, 'synthetic-fresh-1');
  assert.equal(calls, 1, 'a live cached token is not refreshed again');
  // ... and the rotated refresh token replaced the stored one.
  assert.equal(await secrets.getItem(`fugaro.identity-session.${idOf(GOOGLE)}`), 'synthetic-rotated-1');

  // A sign-in whose token still lives answers it without a refresh at all.
  const fresh = new IdentityStore({
    settings: new MemorySettingsStore(),
    sessions: new IdentitySessionStore(new MemorySecureStore()),
    apiKey: 'synthetic-api-key',
    signIn: async () => Object.freeze({ ok: true, session: sessionOf(GOOGLE) }),
    refresh: async () => {
      calls += 1;
      return Object.freeze({ ok: false, code: PROVIDER_CODES.NETWORK, field: null });
    },
  });
  await fresh.add({ provider: 'google.com', idToken: 'token-g' });
  const cached = await fresh.idToken(idOf(GOOGLE));
  assert.equal(cached.idToken, 'synthetic-id-token-synthetic-google-sub');

  // A refresh the exchange refuses is the refusal, and no token is answered.
  const dead = new IdentityStore({
    settings: new MemorySettingsStore(),
    sessions: new IdentitySessionStore(new MemorySecureStore()),
    apiKey: 'synthetic-api-key',
    signIn: async () => Object.freeze({ ok: true, session: sessionOf(GOOGLE, { expiresIn: 0 }) }),
    refresh: async () => Object.freeze({ ok: false, code: PROVIDER_CODES.SIGN_IN_REFUSED, field: 'refreshToken', reason: 'TOKEN_EXPIRED' }),
  });
  await dead.add({ provider: 'google.com', idToken: 'token-g' });
  const refused = await dead.idToken(idOf(GOOGLE));
  assert.equal(refused.code, PROVIDER_CODES.SIGN_IN_REFUSED);
  assert.equal(JSON.stringify(refused).includes('synthetic-refresh'), false);
});

test('an identity whose session item is gone answers session-absent, not a token', async () => {
  const { store, secrets } = makeStore({ signInTable: { 'token-g': sessionOf(GOOGLE, { expiresIn: 0 }) } });
  await store.add({ provider: 'google.com', idToken: 'token-g' });
  await secrets.deleteItem(`fugaro.identity-session.${idOf(GOOGLE)}`);
  const refused = await store.idToken(idOf(GOOGLE));
  assert.equal(refused.code, IDENTITY_CODES.SESSION_ABSENT);
});

test('a records document the store cannot read is a refusal, and the data is left alone', async () => {
  const { store, settings } = makeStore();
  await settings.setItem('fugaro.identities', 'not json');
  const refused = await store.list();
  assert.equal(refused.code, IDENTITY_CODES.UNREADABLE_RECORDS);
  assert.equal(await settings.getItem('fugaro.identities'), 'not json', 'a corrupt document is never wiped silently');

  // A record that is not one this store wrote is the same refusal.
  await settings.setItem('fugaro.identities', JSON.stringify({ version: 1, records: [{ id: 'x', provider: 'google.com', sub: 'y', email: 'e@e.test', authTime: 1, extra: true }] }));
  assert.equal((await store.list()).code, IDENTITY_CODES.UNREADABLE_RECORDS);
});

test('a session write that fails rolls the record back: no identity without its item', async () => {
  const settings = new MemorySettingsStore();
  const failing = {
    async getItem() { return null; },
    async setItem() { throw new Error('full'); },
    async deleteItem() {},
  };
  const store = new IdentityStore({
    settings,
    sessions: new IdentitySessionStore(failing),
    apiKey: 'synthetic-api-key',
    signIn: async () => Object.freeze({ ok: true, session: sessionOf(GOOGLE) }),
    refresh: async () => Object.freeze({ ok: false, code: PROVIDER_CODES.NETWORK, field: null }),
  });
  const refused = await store.add({ provider: 'google.com', idToken: 'token-g' });
  assert.equal(refused.code, IDENTITY_CODES.STORE_FAILED);
  assert.deepEqual((await store.list()).identities, [], 'the record went back with the failed item');
});

test('a removal whose session delete fails keeps the record, so it can be retried', async () => {
  const secrets = new MemorySecureStore();
  const settings = new MemorySettingsStore();
  const store = new IdentityStore({
    settings,
    sessions: new IdentitySessionStore(secrets),
    apiKey: 'synthetic-api-key',
    signIn: async () => Object.freeze({ ok: true, session: sessionOf(GOOGLE) }),
    refresh: async () => Object.freeze({ ok: false, code: PROVIDER_CODES.NETWORK, field: null }),
  });
  await store.add({ provider: 'google.com', idToken: 'token-g' });
  const failing = {
    async getItem(key) { return secrets.getItem(key); },
    async setItem() {},
    async deleteItem() { throw new Error('locked'); },
  };
  const locked = new IdentityStore({
    settings,
    sessions: new IdentitySessionStore(failing),
    apiKey: 'synthetic-api-key',
    signIn: async () => Object.freeze({ ok: false, code: PROVIDER_CODES.NETWORK, field: null }),
    refresh: async () => Object.freeze({ ok: false, code: PROVIDER_CODES.NETWORK, field: null }),
  });
  const refused = await locked.remove(idOf(GOOGLE));
  assert.equal(refused.code, IDENTITY_CODES.STORE_FAILED);
  assert.equal((await store.list()).identities.length, 1, 'the record stays while its item may still be there');
  assert.deepEqual(secrets.keys(), [`fugaro.identity-session.${idOf(GOOGLE)}`]);
});
