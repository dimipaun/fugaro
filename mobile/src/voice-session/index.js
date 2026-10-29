// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The voice session's one door (voice-session plan, Task VS0.3; D205, D206).
//
// A caller imports `src/voice-session/index.js` and gets the machine and the
// event contract. `src/audio/index.js` and `src/voice/index.js` are the
// precedents, and the reason holds here: a file that imports `machine.js`
// directly can build a second machine, and this module's own test pins this
// door's exports exactly.
//
// **What is on the door, and what is deliberately not.** The plan's Files line
// for VS0.3 is `mobile/src/voice-session/` and its sentence is "exports the
// machine and the events, and nothing else": the session controller does not
// exist until VS1.1, and VS1.3 -- the task that constructs it -- widens this
// file and its test's literal list in the same commit. So what a caller has
// here is everything needed to *drive* a session and to read the contract
// (the six kinds, the routes, the states, the reasons, the validator and
// `reduce`), and nothing that reaches a device, a network or a credential:
// `index.test.js` scans this directory for exactly that, including F24's fence
// (a paused session touches no credential) and F9's (no words the user said).
//
// **The machine does not drive itself.** `reduce` answers with a state and a
// verdict; `pipeline.start()`, the native module's `start`/`updateStatus`/
// `setMuted`/`end`, the timers that inject `tick` and P3.5's screen are the
// controller's (VS1.1). Nothing here calls them, which is why this module can
// be tested without a phone.

export {
  AUDIO_ROUTES,
  END_REASONS,
  INTERRUPTION_SOURCES,
  PAUSE_REASONS,
  REMOTE_ACTIONS,
  SERVICE_STOP_REASONS,
  SESSION_EVENTS,
  SESSION_EVENT_FIELDS,
  SESSION_STATES,
  isVoiceSessionEvent,
} from './events.js';

export { INITIAL_SESSION_STATE, MACHINE_CODES, MACHINE_EVENTS, reduce } from './machine.js';
