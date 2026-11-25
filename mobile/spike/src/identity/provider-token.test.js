// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// What each provider's stock sign-in hands back, read into the (email, sub)
// pair the plan's member record is keyed on (task M0; Check 733's U4).

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  PROVIDERS,
  base64UrlBytes,
  claimsOf,
  offeredOn,
  tokenFromApple,
  tokenFromAuthSession,
  utf8,
} from './provider-token.js';

/**
 * A JWT-shaped token whose payload is `claims`. The signature is a word, not a
 * signature: nothing in this spike verifies one (`claimsOf` says so), and no
 * real token is ever written here.
 */
function token(claims) {
  const json = Buffer.from(JSON.stringify(claims), 'utf8').toString('base64url');
  return `spike-header.${json}.spike-signature`;
}

test('the claims are read out of the token, and no other claim comes with them', () => {
  const claims = claimsOf(token({
    sub: '1182994454',
    email: 'someone@example.invalid',
    email_verified: true,
    aud: 'spike.apps.googleusercontent.com',
    iss: 'https://accounts.google.com',
    exp: 1893456000,
    iat: 1893452400,
    // Claims the plan does not want and this module must not copy out.
    name: 'Someone',
    picture: 'https://example.invalid/p.png',
    at_hash: 'xyz',
  }));
  assert.deepEqual(claims, {
    sub: '1182994454',
    email: 'someone@example.invalid',
    email_verified: true,
    aud: 'spike.apps.googleusercontent.com',
    iss: 'https://accounts.google.com',
    exp: 1893456000,
    iat: 1893452400,
  });
});

test('a non-ASCII address survives the decode', () => {
  // The base64url and UTF-8 decoders are written out rather than reached for
  // (`Buffer` and `atob` are not both there on a phone), so they are held to a
  // multi-byte claim: an address with a non-ASCII character in it.
  const email = 'sóméone@example.invalid';
  assert.equal(claimsOf(token({ email })).email, email);
  // ... and the two halves are exercised on their own, including the refusals.
  assert.equal(utf8(base64UrlBytes(Buffer.from('héllo', 'utf8').toString('base64url'))), 'héllo');
  assert.equal(utf8(base64UrlBytes('')), '');
  assert.throws(() => base64UrlBytes('not*base64url'), /not base64url/);
  assert.throws(() => utf8(Uint8Array.from([0xff, 0xfe])), /not UTF-8/);
  assert.throws(() => utf8(Uint8Array.from([0xc3])), /cut/);
});

test('anything that is not a JWT is no claims at all, not a crash', () => {
  for (const value of [undefined, null, '', 'a.b', 'a.!!!.c', 'a..c', `a.${Buffer.from('[]').toString('base64url')}.c`]) {
    assert.equal(claimsOf(value), null, JSON.stringify(value));
  }
});

test('an auth-session answer is a token or a reason', () => {
  const idToken = token({ sub: 's-1', email: 'a@b.invalid' });
  const good = tokenFromAuthSession('google', { type: 'success', params: { id_token: idToken } });
  assert.equal(good.ok, true);
  assert.equal(good.idToken, idToken);
  assert.equal(good.claims.sub, 's-1');

  // The two ways a real run gets an answer that is not a token, each with the
  // reason the screen shows -- a closed sheet is not a refusal.
  const closed = tokenFromAuthSession('microsoft', { type: 'dismiss' });
  assert.equal(closed.ok, false);
  assert.match(closed.reason, /closed without finishing/);
  const refused = tokenFromAuthSession('google', { type: 'error', error: { message: 'access_denied' } });
  assert.equal(refused.ok, false);
  assert.match(refused.reason, /access_denied/);
  const noToken = tokenFromAuthSession('google', { type: 'success', params: { code: 'a-code' } });
  assert.equal(noToken.ok, false);
  assert.match(noToken.reason, /openid scope/);
});

test('Apple answers with an identityToken, and the nonce is carried through', () => {
  const idToken = token({ sub: 'apple-sub', email: 'relay@privaterelay.appleid.com' });
  const good = tokenFromApple({ identityToken: idToken, nonce: 'n-1', user: 'apple-sub' });
  assert.equal(good.ok, true);
  assert.equal(good.provider, 'apple');
  assert.equal(good.nonce, 'n-1');
  assert.equal(good.claims.email, 'relay@privaterelay.appleid.com');
  assert.equal(tokenFromApple({ user: 'apple-sub' }).ok, false);
});

test('Apple is offered on iOS only, and the table names a package for each', () => {
  assert.equal(offeredOn('apple', 'ios'), true);
  assert.equal(offeredOn('apple', 'android'), false);
  assert.equal(offeredOn('google', 'android'), true);
  assert.equal(offeredOn('microsoft', 'ios'), true);
  assert.equal(offeredOn('yahoo', 'ios'), false);
  for (const [name, provider] of Object.entries(PROVIDERS)) {
    assert.ok(provider.package.length > 0, `${name} names the package that signs it in`);
    assert.ok(provider.platforms.length > 0, `${name} is offered somewhere`);
  }
});
