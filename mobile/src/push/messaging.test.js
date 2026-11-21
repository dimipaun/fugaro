// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The Firebase app instance a token is minted on: its tests (mobile plan, Task
// P5.2.8; D259; the design's §3.3 and question 10).
//
// This file is the plan's **fifth** verification sentence and the reason the
// task is security-sensitive: *the app names the Firebase app instance it asks
// for a token on rather than taking the default, because the default is the
// telemetry project's (D259)*. The assertions are about what the SDK is
// ASKED, so the fake records its calls: `getToken` must be called with the app
// OBJECT the name resolved to, and a build that has not registered that app
// must refuse rather than fall back to `[DEFAULT]`.
//
// **The messaging module is faked here, and that is the port's own contract.**
// No test in this repository can load `@react-native-firebase/messaging` (the
// suite runs in a fresh clone with no `node_modules`, `mobile/README.md`), so
// the fake below is the only shape there is -- and the shape it fakes is the
// modular API `mobile/index.js` will hand in: `getApps()` and `getToken(app)`.

import assert from 'node:assert/strict';
import test from 'node:test';

import { MESSAGING_CODES, DEFAULT_APP_NAME, firebaseTokenSource } from './messaging.js';
import { TOKEN_CODES, readPushToken } from './token.js';

/** The name question 10 will answer; this file does not choose it. */
const APP_NAME = 'fugaro-installation-probe';
const TOKEN = 'not-a-real-token-0123456789';

/** The messaging namespace, as the SDK's modular API looks, with a call log. */
function messaging({ apps = [{ name: APP_NAME }], token = TOKEN, throws = false } = {}) {
  const calls = [];
  return {
    calls,
    getApps: () => {
      calls.push('getApps');
      return apps;
    },
    getToken: async (app) => {
      calls.push(`getToken:${app === undefined || app === null ? 'NO-APP' : app.name}`);
      if (throws) throw new Error('the SDK refused');
      return token;
    },
  };
}

test('the token is asked of the NAMED app, never of the default', async () => {
  const sdk = messaging();
  const source = firebaseTokenSource({ messaging: sdk, appName: APP_NAME });
  const read = await readPushToken(source);
  assert.equal(read.ok, true);
  assert.equal(read.token, TOKEN);
  assert.deepEqual(sdk.calls, ['getApps', `getToken:${APP_NAME}`], 'getToken was handed the named app');
  // The whole of D259, as an assertion: `getToken()` with no argument asks the
  // DEFAULT app -- the telemetry project's -- and that call never happens.
  assert.equal(sdk.calls.some((call) => call.includes('NO-APP')), false);
});

test('the default app instance cannot be named, and the refusal says why', () => {
  const sdk = messaging({ apps: [{ name: DEFAULT_APP_NAME }, { name: APP_NAME }] });
  assert.throws(
    () => firebaseTokenSource({ messaging: sdk, appName: DEFAULT_APP_NAME }),
    (error) => error instanceof TypeError && error.message.includes('D259'),
    'the telemetry project’s app is not a name this module will mint a token on',
  );
  // ... and an unnamed app is not one either: it would resolve to whatever the
  // SDK defaults to, which is the same bug with a quieter name.
  for (const appName of [undefined, null, '', 42]) {
    assert.throws(() => firebaseTokenSource({ messaging: sdk, appName }), TypeError, String(appName));
  }
});

test('a build that has not registered the named app refuses, and registers nothing', async () => {
  const sdk = messaging({ apps: [{ name: DEFAULT_APP_NAME }] });
  const source = firebaseTokenSource({ messaging: sdk, appName: APP_NAME });
  const read = await readPushToken(source);
  assert.equal(read.ok, false);
  assert.equal(read.code, MESSAGING_CODES.APP_UNKNOWN);
  assert.equal(sdk.calls.includes('getToken:NO-APP'), false, 'nothing was minted on the default app');
  assert.equal(sdk.calls.some((call) => call.startsWith('getToken:')), false, 'nothing was minted at all');
  // `app-unknown` is an answer and not a throw, because the registry is read at
  // call time: `register()` may be called again on the next open.
});

test('an SDK that refuses becomes a refusal, and an empty token is not one', async () => {
  const source = firebaseTokenSource({ messaging: messaging({ throws: true }), appName: APP_NAME });
  assert.equal((await readPushToken(source)).code, TOKEN_CODES.TOKEN_FAILED);
  const empty = firebaseTokenSource({ messaging: messaging({ token: '' }), appName: APP_NAME });
  assert.equal((await readPushToken(empty)).code, TOKEN_CODES.NO_TOKEN);
});

test('half a port, or a registry that is not a list, is answered rather than trusted', async () => {
  for (const bad of [undefined, null, {}, { getApps: () => [] }, { getToken: async () => TOKEN }]) {
    assert.throws(() => firebaseTokenSource({ messaging: bad, appName: APP_NAME }), TypeError);
  }
  const odd = messaging();
  odd.getApps = () => null;
  const read = await readPushToken(firebaseTokenSource({ messaging: odd, appName: APP_NAME }));
  assert.equal(read.code, MESSAGING_CODES.APP_UNKNOWN);
});
