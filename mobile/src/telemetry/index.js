// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The telemetry module's one door (mobile plan, Tasks P7.2 and P7.3;
// review-levels-ab-jobmax T3.1; D731, D734, D738).
//
// A screen -- or `mobile/src/app/root.js`, which renders the statement today --
// imports `src/telemetry/index.js` and gets the whole of what the app may say
// about its reports: the **switch** (`createCrashReports`, and the reader it is
// built on), the **allow-list** of events and the port they are sent through
// (P7.3, merged ahead of its consumers), the **breadcrumbs** a crash may name,
// and the **statement** and privacy link the shell draws (P7.2). Nothing here
// reaches the SDK: every one of them is a function of the port or of the
// session its caller hands over, which is what lets the whole module load, and
// be tested, on a machine with no device and no native packages
// (`write-path.test.js` rule 5 keeps `@react-native-firebase/*` to
// `mobile/index.js`, and nothing wires it yet -- the entry does that when a
// task puts the switch and the events on the app's startup path).
//
// **Why the door exists.** Each module's `index.js` is its only entry point:
// `notice.js`, `events.js`, `breadcrumbs.js` and `crash.js` are the module's
// parts, and a screen that imported one of them directly would be a screen
// reaching around the surface this file is. It is a re-export list and nothing
// else -- no logic, no default export, no new name -- so it cannot drift from
// the parts it names.

export { CRASH_REASON, crashReportsOf, createCrashReports } from './crash.js';

export {
  ACCOUNT_KINDS,
  EVENT_NAMES,
  EVENT_PARAMETERS,
  EVENTS,
  PAIRING_OUTCOMES,
  REFUSAL_REASONS,
  SCREENS,
  SENDERS,
  TELEMETRY_CODES,
  createTelemetry,
  telemetryEvent,
} from './events.js';

export { BREADCRUMBS, breadcrumb } from './breadcrumbs.js';

export { PRIVACY_URL, STATEMENT, notice, privacyLink } from './notice.js';
