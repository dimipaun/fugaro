// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The Firebase app instance a token is minted on: its tests (mobile plan, Task
// P5.2.8; D259; the design's §3.3 and question 10).
//
// This file is the plan's **fifth** verification sentence and the reason the
// task is security-sensitive: *the app names the Firebase app instance it asks
// for a token on rather than taking the default, because the default is the
// telemetry project's (D259)*. The assertions are about what the SDK is
// ASKED, so the fake records its calls: `getMessaging` must be called with the
// app OBJECT the name resolved to, `getToken` with the instance that call built,
// and a build that has not registered that app must refuse rather than fall back
// to `[DEFAULT]`.
//
// **The SDK is faked here, and that is the port's own contract.** No test in
// this repository can load `@react-native-firebase/*` (the suite runs in a fresh
// clone with no `node_modules`, `mobile/README.md`), so the fake below is the
// only shape there is -- and the shape it fakes is exactly what
// `@react-native-firebase/app@27.1.0` and
// `@react-native-firebase/messaging@27.1.0` export, which is what
// `mobile/index.js` will hand in: the core SDK's `getApps()` and the messaging
// module's `getMessaging(app)` / `getToken(messaging)`. The two namespaces are
// faked separately because the packages separate them: the registry is the core
// SDK's, and the messaging module takes an app instance and lists none.

import assert from 'node:assert/strict';
import test from 'node:test';

import { MESSAGING_CODES, DEFAULT_APP_NAME, firebaseTokenSource } from './messaging.js';
import { TOKEN_CODES, readPushToken } from './token.js';

/** The name question 10 will answer; this file does not choose it. */
const APP_NAME = 'fugaro-installation-probe';
const TOKEN = 'not-a-real-token-0123456789';

/**
 * The two namespaces, as `@react-native-firebase/*`'s modular API really looks,
 * with a call log.
 *
 * `getMessaging` is recorded the way the SDK would be ASKED rather than the way
 * it would answer: the call with no argument -- which resolves the default app,
 * the telemetry project's -- gets its own NO-APP entry, so the assertion below
 * is about the request this module makes and not about a fake refusing a call.
 */
function sdk({ apps = [{ name: APP_NAME }], token = TOKEN, throws = false } = {}) {
  const calls = [];
  return {
    calls,
    app: {
      getApps: () => {
        calls.push('getApps');
        return apps;
      },
    },
    messaging: {
      getMessaging: (app) => {
        if (app === undefined || app === null) {
          calls.push('getMessaging:NO-APP');
          return null;
        }
        calls.push(`getMessaging:${app.name}`);
        return { forApp: app.name };
      },
      getToken: async (instance) => {
        calls.push(`getToken:${instance === undefined || instance === null ? 'NO-INSTANCE' : instance.forApp}`);
        if (throws) throw new Error('the SDK refused');
        return token;
      },
    },
  };
}

test('the token is asked of the NAMED app, never of the default', async () => {
  const fake = sdk();
  const source = firebaseTokenSource({ app: fake.app, messaging: fake.messaging, appName: APP_NAME });
  const read = await readPushToken(source);
  assert.equal(read.ok, true);
  assert.equal(read.token, TOKEN);
  assert.deepEqual(
    fake.calls,
    ['getApps', `getMessaging:${APP_NAME}`, `getToken:${APP_NAME}`],
    'the instance was built from the named app, and the token asked of that instance',
  );
  // The whole of D259, as an assertion: `getMessaging()` with no argument
  // resolves the DEFAULT app -- the telemetry project's -- and that call never
  // happens.
  assert.equal(fake.calls.some((call) => call.includes('NO-APP')), false);
});

test('the default app instance cannot be named, and the refusal says why', () => {
  const fake = sdk({ apps: [{ name: DEFAULT_APP_NAME }, { name: APP_NAME }] });
  assert.throws(
    () => firebaseTokenSource({ app: fake.app, messaging: fake.messaging, appName: DEFAULT_APP_NAME }),
    (error) => error instanceof TypeError && error.message.includes('D259'),
    'the telemetry project’s app is not a name this module will mint a token on',
  );
  // ... and an unnamed app is not one either: it would resolve to whatever the
  // SDK defaults to, which is the same bug with a quieter name.
  for (const appName of [undefined, null, '', 42]) {
    assert.throws(() => firebaseTokenSource({ app: fake.app, messaging: fake.messaging, appName }), TypeError, String(appName));
  }
});

test('a build that has not registered the named app refuses, and registers nothing', async () => {
  const fake = sdk({ apps: [{ name: DEFAULT_APP_NAME }] });
  const source = firebaseTokenSource({ app: fake.app, messaging: fake.messaging, appName: APP_NAME });
  const read = await readPushToken(source);
  assert.equal(read.ok, false);
  assert.equal(read.code, MESSAGING_CODES.APP_UNKNOWN);
  assert.equal(fake.calls.includes('getMessaging:NO-APP'), false, 'no instance was built on the default app');
  assert.equal(fake.calls.some((call) => call.startsWith('getMessaging:')), false, 'no instance was built at all');
  assert.equal(fake.calls.some((call) => call.startsWith('getToken:')), false, 'nothing was minted at all');
  // `app-unknown` is an answer and not a throw, because the registry is read at
  // call time: `register()` may be called again on the next open.
});

test('an SDK that refuses becomes a refusal, and an empty token is not one', async () => {
  const refusing = sdk({ throws: true });
  const source = firebaseTokenSource({ app: refusing.app, messaging: refusing.messaging, appName: APP_NAME });
  assert.equal((await readPushToken(source)).code, TOKEN_CODES.TOKEN_FAILED);
  const emptyFake = sdk({ token: '' });
  const empty = firebaseTokenSource({ app: emptyFake.app, messaging: emptyFake.messaging, appName: APP_NAME });
  assert.equal((await readPushToken(empty)).code, TOKEN_CODES.NO_TOKEN);
});

test('half a port, or a registry that is not a list, is answered rather than trusted', async () => {
  const whole = sdk();
  for (const bad of [
    {},
    { app: whole.app },
    { messaging: whole.messaging },
    { app: {}, messaging: whole.messaging },
    { app: whole.app, messaging: {} },
    { app: whole.app, messaging: { getMessaging: () => ({}) } },
    { app: whole.app, messaging: { getToken: async () => TOKEN } },
  ]) {
    assert.throws(() => firebaseTokenSource({ ...bad, appName: APP_NAME }), TypeError, JSON.stringify(Object.keys(bad)));
  }
  const odd = sdk();
  odd.app.getApps = () => null;
  const read = await readPushToken(firebaseTokenSource({ app: odd.app, messaging: odd.messaging, appName: APP_NAME }));
  assert.equal(read.code, MESSAGING_CODES.APP_UNKNOWN);
});
