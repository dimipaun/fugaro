// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The phone's device token, from the pinned SDK: its tests (mobile plan, Task
// P5.2.8; D259; the design's §3.3 and Q7's answer of 2026-11-21).
//
// **What is pinned here is the call, and it is the default app on purpose.**
// Q7's answer is a relay the publisher holds, and D259 builds the publisher's
// own project's native config into the app -- so the DEFAULT Firebase app is
// the project whose tokens the publisher's relay can deliver to, and asking any
// other app instance for a token was the shape this module had while the answer
// was direct FCM. It could not have worked either: the pinned
// `@react-native-firebase/messaging@27.1.0` declares `hasMultiAppSupport:
// false` and the core SDK's registry throws for a secondary app, so the fake
// below throws for one too, and a wiring that passed an app would be refused by
// the SDK as well as by this test.
//
// **The SDK is faked here, and that is the port's own contract.** No test in
// this repository can load `@react-native-firebase/*` (the suite runs in a
// fresh clone with no `node_modules`, `mobile/README.md`), so the fake is the
// only shape there is -- and it fakes what the pinned package's modular API
// really exports, `getMessaging()`, `registerDeviceForRemoteMessages(messaging)`
// and `getToken(messaging)`, not what a name suggests.
//
// **The order is pinned, and it is the point of the third call.**
// `mobile/firebase.json` turns both auto-registration switches off, so on iOS
// `getToken` rejects an unregistered device -- the pinned SDK's
// `RNFBMessagingHelper.m` answers code `unregistered` -- and the app has to
// register first. A source that called `getToken` first would therefore get no
// token on iOS, which the call log below is what catches.

import assert from 'node:assert/strict';
import test from 'node:test';

import { deviceTokenSource } from './messaging.js';
import { TOKEN_CODES, readPushToken } from './token.js';

const TOKEN = 'not-a-real-device-token-0123456789';

/**
 * The messaging module's modular namespace, with a call log.
 *
 * `getMessaging(app)` takes an **optional** app, and the pinned package's
 * registry throws for anything but the default one -- so this fake throws too,
 * which is what makes "the source passed no app" an assertion rather than a
 * reading of the source. `registered` models the iOS device state the real
 * `getToken` reads: with `firebase.json`'s auto-registration off, a `getToken`
 * before the register call is the `unregistered` rejection the SDK answers.
 */
function sdk({ token = TOKEN, mode = 'answers' } = {}) {
  const calls = [];
  const state = { registered: false };
  return {
    calls,
    state,
    messaging: {
      getMessaging: (app) => {
        if (app !== undefined && app !== null && app.name !== '[DEFAULT]') {
          throw new Error('messaging does not support multiple Firebase Apps');
        }
        calls.push(app === undefined ? 'getMessaging' : `getMessaging:${app.name}`);
        return { app: '[DEFAULT]' };
      },
      registerDeviceForRemoteMessages: async (instance) => {
        calls.push(instance === undefined ? 'register:no-instance' : 'register');
        state.registered = true;
      },
      getToken: async (instance, options) => {
        calls.push(options === undefined ? 'getToken' : `getToken:${JSON.stringify(options)}`);
        if (mode === 'unregistered') throw new Error('unregistered');
        if (!state.registered) {
          // The SDK's own refusal (`RNFBMessagingHelper.m`): with
          // auto-registration off, this is what a `getToken` with no register
          // call gets, so a source that reordered the two would show up here.
          throw new Error('unregistered');
        }
        if (mode === 'throws') throw new Error('the SDK refused');
        return token;
      },
    },
  };
}

test('the device token is minted on the default app, with no project named, after registering', async () => {
  const fake = sdk();
  const read = await readPushToken(deviceTokenSource(fake));
  assert.equal(read.ok, true);
  assert.equal(read.token, TOKEN);
  // Each call carries nothing but the default app's instance: the default app
  // is the publisher's project (D259) and the publisher's relay is what
  // delivers on it, so there is no project to name -- and no app instance to
  // name either, which the control below shows the SDK would refuse.
  //
  // The ORDER is the assertion this test was extended for: the register call
  // comes before `getToken`, because that is what the off auto-registration
  // switches in `firebase.json` leave to the app (the header). Mutation: swap
  // the two calls in `messaging.js` and the fake's `getToken` throws
  // `unregistered`, so this goes red rather than green.
  assert.deepEqual(fake.calls, ['getMessaging', 'register', 'getToken']);
});

test('a source that skipped or reordered the register call gets no token at all', async () => {
  // The positive control for the order above: the same fake refuses a
  // `getToken` that no register preceded, which is the SDK's iOS behaviour the
  // header cites -- so the assertion above is about the order and not about the
  // fake being permissive.
  const fake = sdk();
  await assert.rejects(fake.messaging.getToken(fake.messaging.getMessaging()), /unregistered/);
  // ... and the source's own answer to a refusal that reached it is the
  // token-failed code, never a silent success (the `mode: 'unregistered'`
  // branch of the fake below).
  const refused = await readPushToken(deviceTokenSource(sdk({ mode: 'unregistered' })));
  assert.equal(refused.ok, false);
  assert.equal(refused.code, TOKEN_CODES.TOKEN_FAILED);
});

test('the pinned SDK refuses a secondary app, and so does this fake', async () => {
  // The control for the test above: if `getMessaging` took any app, a source
  // that passed one would be indistinguishable from one that passed none.
  const fake = sdk();
  assert.throws(() => fake.messaging.getMessaging({ name: 'installations-own' }), /multiple Firebase Apps/);
  assert.equal((await readPushToken(deviceTokenSource(fake))).ok, true, 'and the source takes the road the SDK leaves open');
});

test('an SDK that refuses becomes a refusal, and an empty token is not one', async () => {
  assert.equal((await readPushToken(deviceTokenSource(sdk({ mode: 'throws' })))).code, TOKEN_CODES.TOKEN_FAILED);
  assert.equal((await readPushToken(deviceTokenSource(sdk({ token: '' })))).code, TOKEN_CODES.NO_TOKEN);
});

test('half a port is refused at construction rather than at the first registration', () => {
  const whole = sdk().messaging;
  for (const bad of [
    {},
    { messaging: {} },
    { messaging: { getMessaging: () => ({}) } },
    { messaging: { getToken: async () => TOKEN } },
    // The register call is the one a two-method port would be missing, and it
    // is not optional: with `firebase.json`'s auto-registration off, a source
    // without it gets no iOS token at all, so a port missing it is refused here
    // rather than at the first registration on a phone.
    { messaging: { getMessaging: () => ({}), getToken: async () => TOKEN } },
    { messaging: { getMessaging: () => ({}), registerDeviceForRemoteMessages: true, getToken: async () => TOKEN } },
  ]) {
    assert.throws(() => deviceTokenSource(bad), TypeError, JSON.stringify(Object.keys(bad)));
  }
  assert.equal(typeof deviceTokenSource({ messaging: whole }), 'object');
});
