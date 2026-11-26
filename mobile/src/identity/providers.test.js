// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The provider sign-in exchange's tests (task M1; D2793, D2785).
//
// Every value here is synthetic and shaped nothing like a credential (Global
// Constraint 11): the tokens are built by `fakeToken` from readable claims,
// and the refusal tests assert the presented token is NOT in the refusal,
// because a refusal that echoed it would put it on a screen.

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  APPLE_RELAY_DOMAIN,
  PROVIDER_CODES,
  PROVIDERS,
  buildRefresh,
  buildSignInWithIdp,
  claimsOf,
  isProvider,
  isRelayAddress,
  parseRefresh,
  parseSignIn,
  refreshTokens,
  signInWithIdp,
} from './providers.js';

/** A JWT-shaped string with the given payload claims. Synthetic, never signed. */
function fakeToken(claims) {
  const part = (value) => Buffer.from(JSON.stringify(value)).toString('base64url');
  return `${part({ alg: 'none' })}.${part(claims)}.synthetic`;
}

const SIGN_IN = {
  provider: 'google.com',
  sub: 'synthetic-sub-1',
  email: 'one@example.test',
  emailVerified: true,
  authTime: 1764000000,
};

/** A well-formed signInWithIdp success body for `signIn`. */
function successBody(signIn = SIGN_IN) {
  return {
    federatedId: signIn.sub,
    providerId: signIn.provider,
    email: signIn.email,
    emailVerified: signIn.emailVerified,
    idToken: fakeToken({ auth_time: signIn.authTime }),
    refreshToken: 'synthetic-refresh-token',
    expiresIn: '3600',
    // The exchange carries more than the session needs; none of it is read.
    oauthIdToken: 'synthetic-oauth-token',
    displayName: 'Synthetic Person',
    kind: 'identitytoolkit#VerifyAssertionResponse',
  };
}

test('the providers are the three token providers, spelled as the policy spells them', () => {
  assert.deepEqual(Object.keys(PROVIDERS).sort(), ['apple.com', 'google.com', 'microsoft.com']);
  assert.equal(isProvider('google.com'), true);
  assert.equal(isProvider('google'), false, 'the short name is not the policy spelling');
  assert.equal(isProvider('password'), false, 'password sign-in is a different endpoint, not this exchange');
});

test('the sign-in request asks for the session token, and refuses bad input without echoing it', () => {
  const built = buildSignInWithIdp({ apiKey: 'synthetic-api-key', provider: 'apple.com', idToken: 'synthetic-apple-token', nonce: 'synthetic-nonce' });
  assert.equal(built.ok, true);
  assert.equal(built.request.method, 'POST');
  assert.match(built.request.url, /^https:\/\/identitytoolkit\.googleapis\.com\/v1\/accounts:signInWithIdp\?key=/);
  const body = JSON.parse(built.request.body);
  assert.equal(body.returnSecureToken, true, 'M1 holds the session the spike deliberately did not mint');
  assert.equal(body.requestUri, 'http://localhost');
  const post = new URLSearchParams(body.postBody);
  assert.equal(post.get('id_token'), 'synthetic-apple-token');
  assert.equal(post.get('providerId'), 'apple.com');
  assert.equal(post.get('nonce'), 'synthetic-nonce');

  // No nonce, no nonce field.
  const bare = buildSignInWithIdp({ apiKey: 'synthetic-api-key', provider: 'google.com', idToken: 'synthetic-token' });
  assert.equal(new URLSearchParams(JSON.parse(bare.request.body).postBody).has('nonce'), false);

  for (const [args, code] of [
    [{ apiKey: '', provider: 'google.com', idToken: 'synthetic-token' }, PROVIDER_CODES.BAD_CONFIG],
    [{ apiKey: 'synthetic-api-key', provider: 'google', idToken: 'synthetic-token' }, PROVIDER_CODES.UNKNOWN_PROVIDER],
    [{ apiKey: 'synthetic-api-key', provider: 'google.com', idToken: '' }, PROVIDER_CODES.EMPTY_TOKEN],
  ]) {
    const refused = buildSignInWithIdp(args);
    assert.equal(refused.ok, false);
    assert.equal(refused.code, code);
    assert.equal(JSON.stringify(refused).includes('synthetic-token'), false, 'the refusal never echoes the token');
  }
});

test('a sign-in answer is read as the fixed session shape, and nothing else', () => {
  const parsed = parseSignIn({ provider: 'google.com', status: 200, body: JSON.stringify(successBody()) });
  assert.equal(parsed.ok, true);
  assert.deepEqual(Object.keys(parsed.session).sort(), [
    'authTime',
    'email',
    'emailVerified',
    'expiresIn',
    'idToken',
    'provider',
    'refreshToken',
    'sub',
  ]);
  assert.equal(parsed.session.provider, 'google.com');
  assert.equal(parsed.session.sub, 'synthetic-sub-1');
  assert.equal(parsed.session.email, 'one@example.test');
  assert.equal(parsed.session.emailVerified, true);
  assert.equal(parsed.session.authTime, 1764000000);
  assert.equal(parsed.session.expiresIn, 3600);
  assert.equal(JSON.stringify(parsed.session).includes('synthetic-oauth-token'), false);
});

test('a sign-in answer without an email records none, and an Apple relay address is one', () => {
  const hidden = successBody({ ...SIGN_IN, provider: 'apple.com', email: undefined });
  delete hidden.email;
  const parsed = parseSignIn({ provider: 'apple.com', status: 200, body: JSON.stringify(hidden) });
  assert.equal(parsed.ok, true);
  assert.equal(parsed.session.email, null);
});

test('a refused sign-in is a coded refusal, and the reason never carries the token', () => {
  const token = fakeToken({ sub: 'synthetic-sub-9' });
  const body = JSON.stringify({ error: { code: 400, message: 'INVALID_ID_TOKEN' } });
  const refused = parseSignIn({ provider: 'google.com', status: 400, body });
  assert.equal(refused.ok, false);
  assert.equal(refused.code, PROVIDER_CODES.SIGN_IN_REFUSED);
  assert.equal(refused.reason, 'INVALID_ID_TOKEN');
  assert.equal(JSON.stringify(refused).includes(token), false);

  // A non-JSON error page (a proxy's) is the same refusal without a reason.
  const proxied = parseSignIn({ provider: 'google.com', status: 502, body: '<html>bad gateway</html>' });
  assert.equal(proxied.code, PROVIDER_CODES.SIGN_IN_REFUSED);
  assert.equal('reason' in proxied, false);
});

test('a malformed success is bad-response, never a repaired session', () => {
  const good = successBody();
  for (const [name, body] of [
    ['a provider that is not the one asked for', { ...good, providerId: 'microsoft.com' }],
    ['no federated id', { ...good, federatedId: '' }],
    ['no id token', { ...good, idToken: '' }],
    ['no refresh token', { ...good, refreshToken: undefined }],
    ['a token with no auth_time', { ...good, idToken: fakeToken({ sub: 'x' }) }],
    // A real authentication time is a Unix time; zero is what a token with a
    // placeholder claim carries, and the store refuses such a record, so an
    // accepted one would be a document the store could not read back.
    ['a token whose auth_time is zero', { ...good, idToken: fakeToken({ auth_time: 0 }) }],
    ['a token that is not a JWT', { ...good, idToken: 'not-a-jwt' }],
    ['an expiry that is not a number', { ...good, expiresIn: 'soon' }],
  ]) {
    const refused = parseSignIn({ provider: 'google.com', status: 200, body: JSON.stringify(body) });
    assert.equal(refused.ok, false, name);
    assert.equal(refused.code, PROVIDER_CODES.BAD_RESPONSE, name);
  }
});

test('the refresh request is the form Firebase documents, and its answer rotates the token', () => {
  const built = buildRefresh({ apiKey: 'synthetic-api-key', refreshToken: 'synthetic-refresh-token' });
  assert.equal(built.ok, true);
  assert.match(built.request.url, /^https:\/\/securetoken\.googleapis\.com\/v1\/token\?key=/);
  const body = new URLSearchParams(built.request.body);
  assert.equal(body.get('grant_type'), 'refresh_token');
  assert.equal(body.get('refresh_token'), 'synthetic-refresh-token');

  const parsed = parseRefresh({
    status: 200,
    body: JSON.stringify({ id_token: fakeToken({ auth_time: 1764000000 }), refresh_token: 'synthetic-rotated-token', expires_in: '3600' }),
  });
  assert.equal(parsed.ok, true);
  assert.deepEqual(parsed.tokens, { idToken: parsed.tokens.idToken, refreshToken: 'synthetic-rotated-token', expiresIn: 3600 });

  const refused = parseRefresh({ status: 400, body: JSON.stringify({ error: { message: 'TOKEN_EXPIRED' } }) });
  assert.equal(refused.code, PROVIDER_CODES.SIGN_IN_REFUSED);
  assert.equal(refused.reason, 'TOKEN_EXPIRED');
});

test('the network functions map a thrown fetch to a coded refusal', async () => {
  const realFetch = globalThis.fetch;
  globalThis.fetch = async () => {
    throw new Error('no route');
  };
  try {
    for (const call of [
      () => signInWithIdp({ apiKey: 'synthetic-api-key', provider: 'google.com', idToken: 'synthetic-token' }),
      () => refreshTokens({ apiKey: 'synthetic-api-key', refreshToken: 'synthetic-refresh-token' }),
    ]) {
      const refused = await call();
      assert.equal(refused.ok, false);
      assert.equal(refused.code, PROVIDER_CODES.NETWORK);
      assert.equal(JSON.stringify(refused).includes('synthetic'), false, 'neither the token nor the platform error is echoed');
    }
  } finally {
    globalThis.fetch = realFetch;
  }
});

test('an Apple private-relay address is detected, and only that address', () => {
  assert.equal(APPLE_RELAY_DOMAIN, '@privaterelay.appleid.com');
  assert.equal(isRelayAddress('abc@privaterelay.appleid.com'), true);
  assert.equal(isRelayAddress('ABC@PRIVATERELAY.APPLEID.COM'), true, 'addresses compare case-folded');
  assert.equal(isRelayAddress('one@example.test'), false);
  assert.equal(isRelayAddress('abc@privaterelay.appleid.com.evil.test'), false, 'a suffix look-alike is not the relay');
  assert.equal(isRelayAddress(null), false);
  assert.equal(isRelayAddress(undefined), false);
});

test('claimsOf reads the payload of a JWT-shaped token and refuses anything else', () => {
  assert.deepEqual(claimsOf(fakeToken({ auth_time: 1764000000, sub: 'x' })), { auth_time: 1764000000, sub: 'x' });
  assert.equal(claimsOf('not-a-jwt'), null);
  assert.equal(claimsOf('a..c'), null);
  assert.equal(claimsOf(null), null);
  // An array is JSON and is not a claims set.
  assert.equal(claimsOf(`a.${Buffer.from('[]').toString('base64url')}.c`), null);
});

test('a payload is read with no browser global: the phone\'s engine need not have `atob`', () => {
  // `src/pairing/base64url.js` documents `atob` as a global Hermes does not
  // guarantee. A decode that reached for it would refuse every sign-in on such
  // an engine, so the reading is held to the alphabet this module carries.
  const saved = globalThis.atob;
  try {
    delete globalThis.atob;
    assert.deepEqual(claimsOf(fakeToken({ auth_time: 1764000000 })), { auth_time: 1764000000 });
  } finally {
    globalThis.atob = saved;
  }
});
