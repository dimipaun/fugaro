// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The phone's own push token, from the pinned Firebase SDK -- the half of a
// registration the publisher's relay has to be given, and nothing the mailbox
// ever carries (mobile plan, Task P5.2.8; D259; the design is
// docs/mobile/design/push-notifications.md §3.3, §3.5 (b) and §9).
//
// **Q7's answer is a relay the publisher holds, and it is why this file is
// short.** The owner ruled on 2026-11-21 that the app registers with the
// publisher's relay and that the publisher holds the delivery credentials. The
// design's §3.3 is the reason the question existed: FCM HTTP v1 delivers only
// to registration tokens issued for the sending project, and an APNs `.p8` is
// Apple-team-wide, so the installations whose Firebase project is not the
// publisher's cannot be reached directly at all. Under a relay they need not be
// -- the publisher holds the key once and installs hold **a publisher token
// instead** (§3.3), which is `relay.js`'s half of the registration and the next
// paragraph's subject.
//
// **The default Firebase app is right here, and that is D259 read with Q7's
// answer rather than against it.** D259 builds `fugaro-prod`'s native config
// into the app, so the app's default Firebase app is the publisher's project --
// and under the relay that is exactly the project whose tokens the publisher's
// relay can deliver to, because the publisher owns its credentials and the
// relay is the service that holds them. Asking a *different* app instance for a
// token was the shape this module had while the answer was direct FCM, and it
// could not work on the pinned SDK either: `@react-native-firebase/
// messaging@27.1.0` declares `hasMultiAppSupport: false` (`dist/module/
// index.js`) and the core SDK's registry throws for `getMessaging(<any app but
// the default>)` (`@react-native-firebase/app`'s `internal/registry/
// modular.js`: "messaging does not support multiple Firebase Apps"). So the
// device token is asked for on the default app, with no option naming a project
// -- and what must NOT be left to a default is the relay's endpoint, which
// `relay.js` requires to be named.
//
// **What this file answers is a device token, and a device token is not a
// registration.** It is the phone's address at Google for this app's project:
// stable per install, and the thing `relay.js` hands to the publisher's relay
// and never to the mailbox (design §3.9: the app writes nothing about a push
// but its registration, and the registration carries the publisher's token).
//
// **The token is asked for as an act, and the two auto-registration switches
// are off in `mobile/firebase.json` so that it has to be.** SECURITY.md's push
// section states that setting push up is "an act rather than a default", and on
// the pinned SDK a default would register the phone instead: iOS's
// `RNFBMessaging+NSNotificationCenter.m` calls `registerForRemoteNotifications`
// on launch unless `messaging_ios_auto_register_for_remote_messages` is false
// (`getBooleanValue:… defaultValue:YES`), and Android's `build.gradle` writes
// `firebase_messaging_auto_init_enabled` from `messaging_auto_init_enabled`,
// defaulting to true. With both false nothing reaches Google until this file
// calls `registerDeviceForRemoteMessages(instance)` -- which iOS requires before
// `getToken` and answers to (`RNFBMessagingHelper.m` rejects an unregistered
// `getToken` with the code `unregistered`), and which the Android side resolves
// immediately as a no-op, because `getToken` is itself the explicit act there
// (`FirebaseMessaging.getToken()` reads no auto-init flag). A registration that
// were a default would put a token on Google's side with no user act, which is
// the claim this half cannot make.
//
// **The SDK arrives as an object, and this file imports nothing.** `mobile/
// README.md`'s rule: the suite runs in a fresh clone with no `node_modules`, so
// every module a test loads must reach no package -- and `write-path.test.js`'s
// rule 5 keeps `@react-native-firebase/*` to `mobile/index.js`, the entry. What
// the entry hands in is the messaging module's modular namespace as the pinned
// package really exports it: `getMessaging()` (the default app's instance),
// `registerDeviceForRemoteMessages(messaging)` and `getToken(messaging)`. Those
// three are the only methods asked for, and the entry builds this source --
// nothing in `src/push/` reaches for a global.

import { TOKEN_CODES } from './token.js';

function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * A token source over the pinned messaging SDK's default app (see the header).
 *
 * @param {object} options
 * @param {object} options.messaging the messaging module's modular namespace
 *   (`@react-native-firebase/messaging`): `getMessaging()`,
 *   `registerDeviceForRemoteMessages(messaging)` and `getToken(messaging)`
 * @returns {{token: () => Promise<object>}} the source `relayTokenSource` is
 *   built over, and one `readPushToken` understands on its own
 * @throws {TypeError} for a port without the three methods, which is a wiring
 *   bug and refused where it can still be fixed rather than at the first
 *   registration
 */
export function deviceTokenSource({ messaging } = {}) {
  if (messaging === null || typeof messaging !== 'object'
    || typeof messaging.getMessaging !== 'function'
    || typeof messaging.registerDeviceForRemoteMessages !== 'function'
    || typeof messaging.getToken !== 'function') {
    throw new TypeError(
      'deviceTokenSource: the messaging module is required (getMessaging, registerDeviceForRemoteMessages, getToken)',
    );
  }
  return Object.freeze({
    token: async () => {
      // No app, and no options: the default app is the publisher's project
      // (D259, Q7's answer) and naming one is a call the pinned SDK refuses.
      const instance = messaging.getMessaging();
      // The act the flags in `firebase.json` leave to the app (the header): on
      // iOS `getToken` rejects an unregistered device, and on Android this
      // resolves without a call.
      await messaging.registerDeviceForRemoteMessages(instance);
      const token = await messaging.getToken(instance);
      if (typeof token !== 'string' || token === '') return refuse(TOKEN_CODES.NO_TOKEN, 'token');
      return Object.freeze({ ok: true, token });
    },
  });
}
