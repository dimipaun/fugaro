// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The Firebase project a push token is minted for: its tests (mobile plan, Task
// P5.2.8; D259; the design's §3.3 and question 10).
//
// This file is the plan's **fifth** verification sentence and the reason the
// task is security-sensitive: *the app names the Firebase app instance it asks
// for a token on rather than taking the default, because the default is the
// telemetry project's (D259)*. On the pinned SDK that costs TWO names -- the app
// instance for Android, the sender id for iOS -- because the platforms pick the
// project by different arguments and the default of either is the telemetry
// project (`messaging.js`'s header cites the package files). So the fake below
// is faithful to those two native paths, and the assertions are about the
// token's PROJECT: which project a token belongs to is what D259 is about, not
// the shape of the call.
//
// **The fake throws for a secondary app, the way the SDK does.** This module's
// first shape asked `getMessaging(<secondary app>)`, which
// `@react-native-firebase/messaging@27.1.0` cannot answer (`hasMultiAppSupport:
// false`, and the core registry throws "messaging does not support multiple
// Firebase Apps"). `getMessaging` here throws in exactly that case, so the suite
// cannot drift back to a call the SDK closes -- the control assertion below
// shows the fake really does refuse it.
//
// **The SDK is faked here, and that is the port's own contract.** No test in
// this repository can load `@react-native-firebase/*` (the suite runs in a fresh
// clone with no `node_modules`, `mobile/README.md`), so the fake below is the
// only shape there is -- and it fakes what the pinned packages do, not what
// their names suggest: the core SDK's `getApps()`, and the messaging module's
// `getMessaging()` (default app only) and `getToken(messaging, options)`.

import assert from 'node:assert/strict';
import test from 'node:test';

import { MESSAGING_CODES, DEFAULT_APP_NAME, firebaseTokenSource } from './messaging.js';
import { TOKEN_CODES, readPushToken } from './token.js';

/** The name question 10 will answer; this file does not choose it. */
const APP_NAME = 'fugaro-installation-probe';
/** ... nor the installation project's FCM sender id, which is a project number. */
const SENDER_ID = '123456789012';
/** The telemetry project's, carried by the default app (D259). Synthetic. */
const TELEMETRY_SENDER_ID = '210987654321';
const TOKEN = 'not-a-real-token-0123456789';

/**
 * The two namespaces, as the pinned packages really behave, with a call log.
 *
 * `platform` is not a decoration: the native `getToken` picks the project by the
 * app instance on Android and by the sender id on iOS, so a fake that honoured
 * both at once would accept a wiring that works on neither platform. `token`
 * records WHICH project the token would belong to, which is the assertion.
 */
function sdk({ platform = 'android', apps = null, token = TOKEN, throws = false } = {}) {
  const calls = [];
  const registry = apps ?? [
    { name: DEFAULT_APP_NAME, options: { messagingSenderId: TELEMETRY_SENDER_ID } },
    { name: APP_NAME, options: { messagingSenderId: SENDER_ID } },
  ];
  return {
    calls,
    registry,
    app: {
      getApps: () => {
        calls.push('getApps');
        return registry;
      },
    },
    messaging: {
      getMessaging: (app) => {
        // `hasMultiAppSupport: false`: any app but the default throws, which is
        // why the fence asks for none at all and names the project in the
        // options of `getToken` instead.
        if (app !== undefined && app !== null && app.name !== DEFAULT_APP_NAME) {
          throw new Error('messaging does not support multiple Firebase Apps');
        }
        calls.push(app === undefined ? 'getMessaging' : `getMessaging:${app.name}`);
        return { app: DEFAULT_APP_NAME };
      },
      getToken: async (instance, options = {}) => {
        const project = platform === 'android' ? options.appName : options.senderId;
        calls.push(`getToken:${platform}:${project}`);
        if (throws) throw new Error('the SDK refused');
        return token;
      },
    },
  };
}

/** A source over the fake, with the wiring a build that has the answer would do. */
function source(fake, overrides = {}) {
  return firebaseTokenSource({
    app: fake.app,
    messaging: fake.messaging,
    appName: APP_NAME,
    senderId: SENDER_ID,
    ...overrides,
  });
}

test('Android mints on the installation’s app instance, never on the default', async () => {
  const fake = sdk();
  const read = await readPushToken(source(fake));
  assert.equal(read.ok, true);
  assert.equal(read.token, TOKEN);
  assert.deepEqual(
    fake.calls,
    ['getApps', 'getMessaging', `getToken:android:${APP_NAME}`],
    'the instance was asked for with no app (the only shape the SDK takes), and the project named as an option',
  );
  assert.equal(
    fake.calls.includes(`getToken:android:${DEFAULT_APP_NAME}`),
    false,
    'nothing was minted on the telemetry project',
  );
});

test('iOS mints for the installation’s sender, never for the default app’s', async () => {
  const fake = sdk({ platform: 'ios' });
  const read = await readPushToken(source(fake));
  assert.equal(read.ok, true);
  assert.deepEqual(fake.calls, ['getApps', 'getMessaging', `getToken:ios:${SENDER_ID}`]);
  assert.equal(
    fake.calls.includes(`getToken:ios:${TELEMETRY_SENDER_ID}`),
    false,
    'nothing was minted for the telemetry project',
  );
});

test('the pinned SDK refuses a secondary app, and this fake refuses it too', async () => {
  // The control for the two tests above: if `getMessaging` took any app, the
  // fence could name the instance there and the sender id would be pointless.
  const fake = sdk();
  assert.throws(() => fake.messaging.getMessaging(fake.registry[1]), /multiple Firebase Apps/);
  assert.equal((await readPushToken(source(fake))).ok, true, 'and the fence takes the road the SDK leaves open');
});

test('the telemetry project cannot be named on either platform, and each refusal says why', async () => {
  const fake = sdk();
  // Android's argument: the default app instance IS the telemetry project's.
  assert.throws(
    () => source(fake, { appName: DEFAULT_APP_NAME }),
    (error) => error instanceof TypeError && error.message.includes('D259'),
    'the telemetry project’s app is not a name this module will mint a token on',
  );
  // iOS's argument: the sender id the default app carries is read off the
  // registry, so this file needs no project number to refuse it.
  const read = await readPushToken(source(fake, { senderId: TELEMETRY_SENDER_ID }));
  assert.equal(read.ok, false);
  assert.equal(read.code, MESSAGING_CODES.TELEMETRY_SENDER);
  assert.equal(fake.calls.some((call) => call.startsWith('getToken:')), false, 'nothing was minted at all');

  // ... and a name nobody gave is not one either: an unnamed app or sender
  // would resolve to whatever the SDK defaults to, which is the same bug with a
  // quieter name.
  for (const [key, bad] of [
    ['appName', undefined], ['appName', null], ['appName', ''], ['appName', 42],
    ['senderId', undefined], ['senderId', null], ['senderId', ''], ['senderId', 42],
  ]) {
    assert.throws(() => source(fake, { [key]: bad }), TypeError, `${key}=${String(bad)}`);
  }
});

test('a build that has not created the installation’s app refuses, and mints nothing', async () => {
  const fake = sdk({ apps: [{ name: DEFAULT_APP_NAME, options: { messagingSenderId: TELEMETRY_SENDER_ID } }] });
  const read = await readPushToken(source(fake));
  assert.equal(read.ok, false);
  assert.equal(read.code, MESSAGING_CODES.APP_UNKNOWN);
  assert.equal(fake.calls.some((call) => call.startsWith('getToken:')), false, 'nothing was minted at all');
  // `app-unknown` is an answer and not a throw, because the registry is read at
  // call time: `register()` may be called again on the next open.
});

test('an SDK that refuses becomes a refusal, and an empty token is not one', async () => {
  assert.equal((await readPushToken(source(sdk({ throws: true })))).code, TOKEN_CODES.TOKEN_FAILED);
  assert.equal((await readPushToken(source(sdk({ token: '' })))).code, TOKEN_CODES.NO_TOKEN);
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
    assert.throws(
      () => firebaseTokenSource({ ...bad, appName: APP_NAME, senderId: SENDER_ID }),
      TypeError,
      JSON.stringify(Object.keys(bad)),
    );
  }
  const odd = sdk();
  odd.app.getApps = () => null;
  assert.equal((await readPushToken(source(odd))).code, MESSAGING_CODES.APP_UNKNOWN);
});
