// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The voice switch's one door (mobile plan, Tasks P5.3.6 and P5.3.7; D111,
// D118, D123, D127; the design is
// docs/mobile/design/voice-evaluation-switch.md §4.1-§4.5 and §5).
//
// A screen imports `src/voice-switch/index.js` and gets the switch, the badge
// and the egress path -- `createVoiceSwitch`, `renderIndicator`,
// `createVoiceHop` and `createVoiceEgress` -- and not the files that compose
// the storage key, judge a record, edit a consent list or hold the client's
// own vocabulary. `src/voice/index.js` and `src/accounts/index.js` are the
// precedents, and the reason holds here: a screen that imported `settings.js`
// could compose a key of its own and write a second record, and this module's
// own `index.test.js` pins this list so that a name appearing here is a
// decision rather than an accident.
//
// **What is deliberately NOT here.** No host and no credential. The hop's
// address arrives as `createVoiceHop`'s `baseUrl` and is checked whole against
// `shared/url`'s allow-list on every call, whose default is the empty list --
// the hop's host is the owner's to name (P5.3, item 2), so this directory ships
// none, and a tree with no host refuses every URL with `allow-list-empty`
// rather than sending one somewhere nobody named. The client's own request
// vocabulary (`HOP_PATHS`, `HOP_HEADER`, `HOP_TEXT_TYPE`, `MAX_ANSWER_BYTES`)
// stays on `hop.js` and out of the door for the same reason a screen has no
// business composing a hop URL: what a caller needs to branch on is
// `HOP_CODES` and `HOP_REASON`, and the wire itself is not a caller's.
//
// **What a caller does with `send`.** It reads true only for a live,
// admissible, consented, not-already-in-flight remote route, and the egress
// path is the one thing that may act on it: `createVoiceEgress(...).send()`
// reads the badge, keeps §5's ten rules, and answers locally on every failure.

export {
  DEFAULT_VOICE_SETTINGS,
  DIRECTIONS,
  VOICE_CODES,
  VOICE_RECORD_FIELDS,
  VOICE_SETTINGS_PREFIX,
  voiceSettingsKey,
} from './settings.js';

export { CONSENT_FIELDS, CONSENT_MAX, hasConsent, withConsent, withoutConsent } from './consent.js';

export { INDICATOR, INDICATOR_FIELDS, failClosedIndicator, renderIndicator } from './indicator.js';

export { createVoiceSwitch } from './switch.js';

export { EGRESS_CODE, EGRESS_OFFER, EGRESS_RULES, createVoiceEgress, permit, readBackFor } from './egress.js';

export { DESTRUCTIVE_VERBS, REDACTED } from './egress.js';

export { HOP_CODES, HOP_REASON, createVoiceHop } from './hop.js';
