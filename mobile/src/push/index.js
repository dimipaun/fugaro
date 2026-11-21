// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The push module's one door (mobile plan, Task P5.2.8; D113, D123, D160, D166,
// D259; the design is docs/mobile/design/push-notifications.md §3.3, §3.5 (b),
// §3.8 and §6).
//
// A screen imports `src/push/index.js` and gets `createPush` -- the switches and
// the registration over one account -- plus the vocabulary it renders:
// `pushScreen` for the two facts it shows (registered or not, and what the
// switches are), the state and code lists those answers are made of, and
// `firebaseTokenSource`, which names the Firebase project the token is minted
// for (D259) -- the app instance and the sender id, one per platform. **That
// last one is built by the app's entry and not here**: `mobile/index.js` is the
// only file that may import `@react-native-firebase/*` (`write-path.test.js`
// rule 5), so it is where the two SDK namespaces, the app name and the sender id
// arrive -- and no task has put push on a screen yet, so nothing there builds it
// today. Until something does, a caller that already has them builds
// this door itself, which is what its tests do.
//
// **What is deliberately NOT here.** No Firebase SDK (`messaging.js` is handed
// the messaging namespace as an object, because the suite runs in a fresh clone
// with no `node_modules` and because `write-path.test.js`'s rule 5 keeps
// `@react-native-firebase/*` to `mobile/index.js`), no store, no connection and
// no key: `createPush` takes the mailbox client and the settings store the app
// already builds. The `client.js` file of this directory's neighbour is the app's
// ONE write path (D113), so nothing here builds a path of its own -- the
// fingerprint of a second write path is exactly what this door is shaped to
// avoid.
//
// **A caller reads two outcomes, not one.** `setControl` and friends answer
// `{ ok, prefs, published }`: the switch was STORED (`ok`), and the registration
// that carries it either landed (`published.ok`) or did not -- §3.8's controls
// reach the server only inside that message, so a screen that showed one for
// both would claim a silence the server has never heard of.

export { createPush } from './push.js';

export {
  CONTROL_CODES,
  CONTROL_DEFAULTS,
  CONTROL_NAMES,
  PUSH_SETTINGS_PREFIX,
  WINDOW_FIELDS,
  createPushControls,
  pushSettingsKey,
} from './controls.js';

export {
  MESSAGE_CODES,
  REGISTRATION_FIELDS,
  REGISTRATION_KIND,
  REGISTRATION_TEXT,
  judgeField,
} from './message.js';

export {
  DEFAULT_APP_NAME,
  MESSAGING_CODES,
  firebaseTokenSource,
} from './messaging.js';

export { PUSH_STATES, createPushRegistration } from './registration.js';

export { CONTROL_LINES, PUSH_LINES, PUSH_WAITING, pushScreen, pushStateLine } from './screen.js';

export { TOKEN_CODES, MemoryTokenSource, readPushToken } from './token.js';
