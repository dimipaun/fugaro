// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The voice session's one door (voice-session plan, Tasks VS0.3 and VS1.3;
// D205, D206, D213).
//
// A caller imports `src/voice-session/index.js` and gets the machine, the
// event contract and the controller the app talks to. `src/audio/index.js` and
// `src/voice/index.js` are the precedents, and the reason holds here: a file
// that imports `machine.js` directly can build a second machine, and this
// module's own test pins this door's exports exactly.
//
// **What is on the door, and what is deliberately not.** VS0.3's sentence is
// "exports the machine and the events, and nothing else", and VS1.3 -- the task
// that constructs the session -- widened it by exactly one file in the same
// commit as the export: the controller (`session.js`, VS1.1) and the codes it
// answers with are here, because `mobile/index.js` is the app's one
// construction site for a session and a caller that had to reach around the
// door for it would be the second one. So what a caller has here is everything
// needed to *build* a session, to *drive* it and to read the contract (the six
// kinds, the routes, the states, the reasons, the validator and `reduce`), and
// nothing that reaches a device, a network or a credential: `index.test.js`
// scans this directory for exactly that, including F24's fence (a paused
// session touches no credential) and F9's (no words the user said).
//
// **The fake port is not on this door, and that is the same rule.** A class on
// the door is a thing to construct, and the one constructible object this
// module would hand out is §5's port -- which is a *phone* on a store build and
// VS1.2's fake on the one the app runs today. The door carries the contract
// (`NATIVE_METHODS`, `isVoiceSessionNative`), and `mobile/index.js` -- the one
// file that knows what a device is -- is what decides which of the two answers
// it, exactly as it decides the Keychain port and the AsyncStorage port for the
// account store.
//
// **The machine does not drive itself.** `reduce` answers with a state and a
// verdict; `pipeline.start()`, the native module's `start`/`updateStatus`/
// `setMuted`/`end`, the timers that inject `tick` and P3.5's screen are the
// controller's (VS1.1), and the controller takes every one of them as an
// argument. Nothing here reaches a phone, which is why this module can be
// tested without one.

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

export { SESSION_CODES, createVoiceSession } from './session.js';
