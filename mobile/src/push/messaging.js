// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The Firebase app instance a push token is minted on, named rather than
// defaulted (mobile plan, Task P5.2.8; D259; the design is
// docs/mobile/design/push-notifications.md §3.3 and question 10).
//
// **The default Firebase app belongs to the telemetry project, and a token
// minted for it is a token the installation's sender cannot use.** D259
// (2026-10-19) builds `fugaro-prod`'s native config into the app so that the
// plan's telemetry checks are about the project the reports really reach, and
// the consequence for push is exact: `getToken()` with no argument asks the
// DEFAULT app, which is the telemetry project's, and FCM HTTP v1 sends only to
// tokens issued for the sending project (§3.3). So the token has to be asked
// for on a **named** app instance -- a second app the app's own wiring builds
// from the installation's configuration -- and this module is the fence that
// makes that impossible to forget: the name is required at construction, the
// default name is refused as a wiring bug, and `getToken` is only ever called
// with an app object pulled out of the SDK's own registry by that name.
//
// **Which name, and which project, is question 10's answer and not this task's
// choice.** The design puts three shapes to the owner (one app instance per
// installation, a second project named at runtime, or Q7's relay) and this
// module names **none** of them: `appName` arrives as an argument, so the
// answer becomes one wiring line in `mobile/index.js` and no line of code here.
// That is also what keeps the check honest -- a build that has not been given
// the answer REFUSES to register (`app-unknown`, because no app of that name is
// registered) rather than quietly registering on the telemetry project's.
//
// **The SDK arrives as an object, and this file imports nothing.** `mobile/
// README.md`'s rule: the suite runs in a fresh clone with no `node_modules`, so
// every module a test loads must reach no package -- and `write-path.test.js`'s
// rule 5 keeps `@react-native-firebase/*` to `mobile/index.js`, the entry. The
// object this module is handed is the messaging module's own namespace (the
// modular API: `getApps()`, `getToken(app)`), and the only methods it uses are
// the two named in the constructor's check.

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
});

function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * The app the token is asked for on, or `null` when this build has not
 * registered one of that name.
 *
 * `getApps()` is asked rather than `getApp(name)`, which throws for a name
 * nobody registered: the registry read is an answer in both directions, so the
 * refusal above stays a refusal and no SDK error has to be classified here.
 */
function namedApp(messaging, appName) {
  const apps = messaging.getApps();
  if (!Array.isArray(apps)) return null;
  return apps.find((app) => app !== null && typeof app === 'object' && app.name === appName) ?? null;
}

/**
 * A token source that asks a NAMED Firebase app instance for its registration
 * token (see the header).
 *
 * @param {object} options
 * @param {object} options.messaging the messaging SDK's modular namespace:
 *   `getApps()` and `getToken(app)`
 * @param {string} options.appName the app instance question 10 names. Required,
 *   and never `[DEFAULT]`: the default is the telemetry project's (D259)
 * @returns {{token: () => Promise<object>}} the source `readPushToken` takes
 * @throws {TypeError} for a port without the two methods, for an unnamed app
 *   instance, and for `[DEFAULT]` -- all three are wiring bugs, and each is
 *   refused where it can still be fixed rather than at the first registration
 */
export function firebaseTokenSource({ messaging, appName } = {}) {
  if (messaging === null || typeof messaging !== 'object'
    || typeof messaging.getApps !== 'function' || typeof messaging.getToken !== 'function') {
    throw new TypeError('firebaseTokenSource: the messaging module is required (getApps, getToken)');
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
  return Object.freeze({
    token: async () => {
      const app = namedApp(messaging, appName);
      if (app === null) return refuse(MESSAGING_CODES.APP_UNKNOWN, 'app');
      const token = await messaging.getToken(app);
      if (typeof token !== 'string' || token === '') return refuse(TOKEN_CODES.NO_TOKEN, 'token');
      return Object.freeze({ ok: true, token });
    },
  });
}
