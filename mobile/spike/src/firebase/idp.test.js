// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The `signInWithIdp` request and the reading of its answer (task M0; Check
// 733's U4). No token in any output, in either direction.

import assert from 'node:assert/strict';
import test from 'node:test';

import { IDP_ENDPOINT, buildSignInWithIdp, parseIdpResponse, signInWithIdp } from './idp.js';

// A token-shaped string that is obviously not a real one, so a leak is a leak
// this test can name and no credential is ever written here.
const TOKEN = 'spike-test-token-not-a-credential';
const KEY = 'spike-test-api-key-not-a-credential';

test('the request is a POST to the exchange, with the key in the query', () => {
  const request = buildSignInWithIdp({ apiKey: KEY, provider: 'google', idToken: TOKEN });
  assert.equal(request.method, 'POST');
  assert.equal(request.url, `${IDP_ENDPOINT}?key=${KEY}`);
  assert.equal(request.headers['Content-Type'], 'application/json');

  const body = JSON.parse(request.body);
  assert.equal(body.postBody, `id_token=${TOKEN}&providerId=google.com`);
  assert.equal(body.requestUri, 'http://localhost');
  assert.equal(body.returnIdpCredential, true);
});

test('the exchange is not asked for a Firebase credential', () => {
  // U4 asks whether the provider token is accepted, and nothing else. Asking
  // for a secure token would put a live ID token and refresh token into the
  // spike's memory and into anything it prints, for a reading that does not
  // need them (D232, D2793).
  const body = JSON.parse(buildSignInWithIdp({ apiKey: KEY, provider: 'apple', idToken: TOKEN, nonce: 'n-1' }).body);
  assert.equal(body.returnSecureToken, false);
  assert.equal(body.postBody, `id_token=${TOKEN}&providerId=apple.com&nonce=n-1`);
});

test('an unset or empty field is refused by name, and the value is never repeated', () => {
  for (const [args, named] of [
    [{ provider: 'google', idToken: TOKEN }, 'Firebase Web API key'],
    [{ apiKey: '', provider: 'google', idToken: TOKEN }, 'Firebase Web API key'],
    [{ apiKey: KEY, provider: 'google' }, 'google ID token'],
    [{ apiKey: KEY, provider: 'google', idToken: '  ' }, 'google ID token'],
    [{ apiKey: KEY, provider: 'yahoo', idToken: TOKEN }, 'yahoo'],
  ]) {
    assert.throws(() => buildSignInWithIdp(args), (error) => {
      assert.ok(error.message.includes(named), `message does not name ${named}: ${error.message}`);
      // The one thing the message must never carry: what was mistyped into it.
      assert.equal(error.message.includes(TOKEN), false, 'the token is not repeated in the error');
      assert.equal(error.message.includes(KEY), false, 'the key is not repeated in the error');
      return true;
    });
  }
});

test('a success body is read for its fields, and none of its tokens comes out', () => {
  // Firebase's own success body, as `signInWithIdp` writes it when a secure
  // token was asked for: the answer must name its fields rather than spread the
  // body, or the screen, the clipboard and the draft would carry live
  // credentials out of a reading that was only about acceptance.
  const answer = parseIdpResponse({
    status: 200,
    body: JSON.stringify({
      federatedId: '1182994454...',
      providerId: 'google.com',
      localId: 'abcdef',
      emailVerified: true,
      email: 'someone@example.invalid',
      displayName: 'Someone',
      photoUrl: 'https://example.invalid/p.png',
      idToken: 'firebase-id-token',
      refreshToken: 'firebase-refresh-token',
      oauthIdToken: 'provider-id-token',
      oauthAccessToken: 'provider-access-token',
      expiresIn: '3600',
    }),
  });
  assert.equal(answer.ok, true);
  assert.equal(answer.httpStatus, 200);
  assert.equal(answer.email, 'someone@example.invalid');
  assert.equal(answer.providerId, 'google.com');
  for (const leaked of ['idToken', 'refreshToken', 'oauthIdToken', 'oauthAccessToken', 'expiresIn']) {
    assert.equal(leaked in answer, false, `${leaked} must not be in a parsed answer`);
  }
  assert.equal(JSON.stringify(answer).includes('firebase-refresh-token'), false);
});

test('a refusal carries the status and Firebase\'s own error text', () => {
  const answer = parseIdpResponse({
    status: 400,
    body: JSON.stringify({
      error: { code: 400, message: 'INVALID_IDP_RESPONSE : Unable to parse Google id_token', errors: [{ message: 'x' }] },
    }),
  });
  assert.equal(answer.ok, false);
  assert.equal(answer.httpStatus, 400);
  assert.equal(answer.error.code, 400);
  // The message is the finding: the draft records the refusal in Firebase's own
  // words, which is what tells the plan *which* refusal this was.
  assert.equal(answer.error.message, 'INVALID_IDP_RESPONSE : Unable to parse Google id_token');
});

test('a status that is not 200 is a refusal even with a clean body', () => {
  // A proxy's HTML error page, a 403 from an API-key restriction: the body is
  // not JSON and the exchange did not happen, so the outcome is the status's.
  const answer = parseIdpResponse({ status: 403, body: '<html>forbidden</html>' });
  assert.equal(answer.ok, false);
  assert.equal(answer.httpStatus, 403);
  assert.equal(answer.error.message, '<html>forbidden</html>');
});

test('the exchange runs over the transport it is handed, and a dead network is a reading', async () => {
  const seen = [];
  const http = async (url, init) => {
    seen.push({ url, init });
    return { status: 200, text: async () => JSON.stringify({ providerId: 'microsoft.com', email: 'a@b.invalid' }) };
  };
  const answer = await signInWithIdp(http, buildSignInWithIdp({ apiKey: KEY, provider: 'microsoft', idToken: TOKEN }));
  assert.equal(answer.ok, true);
  assert.equal(answer.providerId, 'microsoft.com');
  assert.equal(seen.length, 1);
  assert.equal(seen[0].url, `${IDP_ENDPOINT}?key=${KEY}`);

  // ... and a transport that throws says so on the screen rather than taking
  // the app down: an unreachable Firebase is a thing Check 733 records.
  const dead = await signInWithIdp(async () => { throw new Error('Network request failed'); }, buildSignInWithIdp({ apiKey: KEY, provider: 'google', idToken: TOKEN }));
  assert.equal(dead.ok, false);
  assert.equal(dead.httpStatus, null);
  assert.equal(dead.error.message, 'the request could not be made: Network request failed');
  assert.equal(JSON.stringify(dead).includes(TOKEN), false);
});
