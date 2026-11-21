// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The Firebase project a push token is minted for, named rather than defaulted
// (mobile plan, Task P5.2.8; D259; the design is
// docs/mobile/design/push-notifications.md §3.3 and question 10).
//
// **The default Firebase app belongs to the telemetry project, and a token
// minted for it is a token the installation's sender cannot use.** D259
// (2026-10-19) builds `fugaro-prod`'s native config into the app so that the
// plan's telemetry checks are about the project the reports really reach, and
// the consequence for push is exact: FCM HTTP v1 sends only to tokens issued
// for the sending project (§3.3), and the default app's own sender is the
// telemetry project's. So the token has to be minted for the **installation's**
// project -- one the app's default config does not carry -- and this module is
// the fence that makes that impossible to forget.
//
// **What naming the project costs on the pinned SDK, verified against it.** A
// secondary app instance is not available here: `@react-native-firebase/
// messaging@27.1.0` declares `hasMultiAppSupport: false` (`dist/module/
// index.js`), and the core SDK's registry throws for `getMessaging(<any app but
// the default>)` (`@react-native-firebase/app`'s `internal/registry/
// modular.js`: "messaging does not support multiple Firebase Apps"). What does
// work is the instance's own `getToken(options)`, which resolves
//
//   const appName = options?.appName || this.app.name;
//   const senderId = options?.senderId || this.app.options.messagingSenderId;
//
// and hands both to the native side -- where the two platforms pick the project
// by DIFFERENT arguments: Android mints on the app instance
// (`FirebaseApp.getInstance(appName).get(FirebaseMessaging.class)`, the sender
// id unused), iOS mints for the sender (`retrieveFCMTokenForSenderID:`, the app
// name unused). Neither may be left to default, because the default of both is
// the telemetry project -- so **both** are required here, and each is refused
// when it names the telemetry project: `appName` may not be `[DEFAULT]`, and
// `senderId` may not be the sender id the default app carries.
//
// **Which instance, and which sender, is question 10's answer and not this
// task's choice.** The design puts three shapes to the owner (one app instance
// per installation, a second project named at runtime, or Q7's relay) and this
// module names **none** of them: `appName` and `senderId` arrive as arguments,
// so the answer becomes wiring lines in `mobile/index.js` and no line of code
// here. That is also what keeps the check honest -- a build that has not been
// given the answer REFUSES to register (`app-unknown` when the instance is not
// registered, `telemetry-sender` when the sender is the default app's) rather
// than quietly registering on the telemetry project's.
//
// **The SDK arrives as objects, and this file imports nothing.** `mobile/
// README.md`'s rule: the suite runs in a fresh clone with no `node_modules`, so
// every module a test loads must reach no package -- and `write-path.test.js`'s
// rule 5 keeps `@react-native-firebase/*` to `mobile/index.js`, the entry. What
// the entry hands in is the **two** namespaces the pinned packages really
// export, because the registry is not the messaging module's: `app` is
// `@react-native-firebase/app`'s modular namespace (`getApps()`), and
// `messaging` is `@react-native-firebase/messaging`'s (`getMessaging()`,
// `getToken(messaging, options)`). The three methods named in the constructor's
// checks are the only ones either is asked for.

import { TOKEN_CODES } from './token.js';

/** The name the SDK gives the default app -- the telemetry project's (D259). */
export const DEFAULT_APP_NAME = '[DEFAULT]';

/** The refusal codes this module answers with, beside `token.js`'s. */
export const MESSAGING_CODES = Object.freeze({
  /**
   * The named app instance is not registered. `app-unknown` and not a throw:
   * the registry is read at call time, an app instance is created at startup,
   * and "this build has not created the instance question 10 names" is a fact
   * about the build rather than a broken port. It is answered, so the app can
   * say so and try again.
   */
  APP_UNKNOWN: 'app-unknown',
  /**
   * The sender id named is the one the default app carries, which on this app is
   * the telemetry project's (D259). Answered rather than thrown, because the
   * value it is compared against is read off the registry at call time -- the
   * same reason `app-unknown` is a code -- and because it is the one refusal
   * that would otherwise mint a token no sender the installation has can use.
   */
  TELEMETRY_SENDER: 'telemetry-sender',
});

function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * The registered apps, read once per attempt, or `null` when the registry cannot
 * be read at all.
 *
 * `getApps()` is asked rather than `getApp(name)`, which throws for a name nobody
 * registered: the registry read is an answer in both directions, so the refusals
 * below stay refusals and no SDK error has to be classified here. It comes from
 * the core SDK's namespace, which is where the registry lives; the messaging
 * module takes an app instance and has none to list.
 */
function registryOf(app) {
  const apps = app.getApps();
  return Array.isArray(apps) ? apps : null;
}

/** The app registered under `appName`, or `null` when there is none. */
function appNamed(apps, appName) {
  if (apps === null) return null;
  return apps.find((one) => one !== null && typeof one === 'object' && one.name === appName) ?? null;
}

/**
 * The sender id the default app carries -- the telemetry project's, by D259's
 * own construction -- or `null` when the registry cannot say.
 *
 * Read rather than written down: this file has no project number to hardcode,
 * and the app D259 builds from `fugaro-prod`'s config is the one thing that
 * knows it. `null` means the question could not be asked (no default app
 * registered, or one whose config carries no sender), not that the sender is
 * safe -- which is why the caller keeps the other refusal (`app-unknown`) and
 * why the sender id was required to be named in the first place.
 */
function defaultSenderId(apps) {
  const sender = appNamed(apps, DEFAULT_APP_NAME)?.options?.messagingSenderId;
  return typeof sender === 'string' && sender !== '' ? sender : null;
}

/**
 * A token source that asks for the installation's project and never for the
 * telemetry project's (see the header).
 *
 * @param {object} options
 * @param {object} options.app the core SDK's modular namespace
 *   (`@react-native-firebase/app`): `getApps()`, the registry both the instance
 *   and the telemetry project's sender id are read from
 * @param {object} options.messaging the messaging module's modular namespace
 *   (`@react-native-firebase/messaging`): `getMessaging()` and
 *   `getToken(messaging, options)`
 * @param {string} options.appName the app instance question 10 names. Required,
 *   and never `[DEFAULT]`: the default is the telemetry project's (D259), and on
 *   Android this argument is the one that picks the project
 * @param {string} options.senderId the installation project's FCM sender id.
 *   Required, and never the one the default app carries: on iOS this argument is
 *   the one that picks the project
 * @returns {{token: () => Promise<object>}} the source `readPushToken` takes
 * @throws {TypeError} for a port without the three methods, for an app instance
 *   or a sender id that is not named, and for `[DEFAULT]` -- all five are wiring
 *   bugs, and each is refused where it can still be fixed rather than at the
 *   first registration
 */
export function firebaseTokenSource({ app, messaging, appName, senderId } = {}) {
  if (app === null || typeof app !== 'object' || typeof app.getApps !== 'function') {
    throw new TypeError('firebaseTokenSource: the app SDK is required (getApps)');
  }
  if (messaging === null || typeof messaging !== 'object'
    || typeof messaging.getMessaging !== 'function' || typeof messaging.getToken !== 'function') {
    throw new TypeError('firebaseTokenSource: the messaging module is required (getMessaging, getToken)');
  }
  if (typeof appName !== 'string' || appName === '') {
    throw new TypeError('firebaseTokenSource: the Firebase app instance must be named');
  }
  if (appName === DEFAULT_APP_NAME) {
    throw new TypeError(
      `firebaseTokenSource: ${DEFAULT_APP_NAME} is the telemetry project's app (D259), and a token minted ` +
        'for it is not one the installation\'s sender can use -- name the app instance question 10 answers',
    );
  }
  if (typeof senderId !== 'string' || senderId === '') {
    throw new TypeError(
      'firebaseTokenSource: the installation project\'s sender id must be named: it is the only argument that ' +
        'picks the project on iOS, and leaving it out takes the default app\'s -- the telemetry project\'s (D259)',
    );
  }
  return Object.freeze({
    token: async () => {
      const apps = registryOf(app);
      if (appNamed(apps, appName) === null) return refuse(MESSAGING_CODES.APP_UNKNOWN, 'app');
      const telemetry = defaultSenderId(apps);
      if (telemetry !== null && telemetry === senderId) return refuse(MESSAGING_CODES.TELEMETRY_SENDER, 'sender');
      // No app object is passed: `getMessaging(<an app>)` throws for anything
      // but the default one, and the project is named in the options instead --
      // `appName` for Android, `senderId` for iOS, both required above.
      const token = await messaging.getToken(messaging.getMessaging(), { appName, senderId });
      if (typeof token !== 'string' || token === '') return refuse(TOKEN_CODES.NO_TOKEN, 'token');
      return Object.freeze({ ok: true, token });
    },
  });
}
