// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The voice switch's one door (mobile plan, Task P5.3.6; D123, D127; the design
// is docs/mobile/design/voice-evaluation-switch.md §4.1-§4.3).
//
// A screen imports `src/voice-switch/index.js` and gets the switch and the
// badge -- `createVoiceSwitch` and `renderIndicator` -- and not the files that
// compose the storage key, judge a record or edit a consent list.
// `src/voice/index.js` and `src/accounts/index.js` are the precedents, and the
// reason holds here: a screen that imported `settings.js` could compose a key
// of its own and write a second record, and this module's own `index.test.js`
// pins this list so that a name appearing here is a decision rather than an
// accident.
//
// **What is deliberately NOT here.** The egress path (P5.3.7) -- the hop client
// and the ten never-send rules -- is not in this directory yet, and neither is
// the voice screen's rendering of the badge; this door is the switch and the
// answer about where an utterance would go, and the sending is the next task's.
// What a caller does with `send` today is nothing, on purpose: it reads true
// only for a live, admissible, consented, not-already-in-flight remote route,
// and there is nothing yet that may act on it.

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
