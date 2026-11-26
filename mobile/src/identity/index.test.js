// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The identity module's door, asserted as an exact set (task M1).
//
// The exchanges' request builders and parsers stay off the door: a screen
// holds the store, and the store holds the exchange. What a screen may name is
// the store, the provider table, the relay question and the two code sets.

import assert from 'node:assert/strict';
import test from 'node:test';

import * as identity from './index.js';

test('the door exports the store, the providers and the relay question, and no port', () => {
  assert.deepEqual(Object.keys(identity).sort(), [
    'APPLE_RELAY_DOMAIN',
    'IDENTITY_CODES',
    'IdentityStore',
    'PROVIDERS',
    'PROVIDER_CODES',
    'isProvider',
    'isRelayAddress',
  ]);
  // The network is the store's to open: no request builder and no fetch is
  // reachable from the door, so a screen cannot hand a token to a URL itself.
  assert.equal('signInWithIdp' in identity, false);
  assert.equal('buildSignInWithIdp' in identity, false);
});
