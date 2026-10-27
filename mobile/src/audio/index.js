// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The audio module's one door (mobile plan, Task P3.4; D122, D123, D126).
//
// Everything the rest of the app needs from `src/audio/`, re-exported here and
// nowhere else, so that a screen imports `src/audio/index.js` and gets the
// three ports, their fakes and the pipeline -- not `pipeline.js`'s internals.
// `src/accounts/index.js` is the precedent and its reason holds here: a file
// that imports `tts.js` directly can build a second output port, and the
// module's own test pins this door's exports exactly, which is what makes
// "interfaces and fakes only, no engine" a fact rather than a promise.
//
// **What P3.4 ships, and what it deliberately does not.** The plan's Files line
// is `mobile/src/audio/` and its verification is the three sentences in
// `pipeline.js`'s header; the engine is a later task's. There is **no capture
// path, no speech engine and no audio decoder here**, and none can arrive
// quietly:
//
//   * the door's exports are asserted against a literal list (`index.test.js`),
//     so a fifth port or a "real" implementation is a deliberate edit to that
//     test;
//   * a scan over this directory asserts that no file imports **any** package
//     -- not `expo-av`, not `react-native-voice`, not a cloud client -- and
//     reaches outside `mobile/` only into `shared/` (D119, D125);
//   * the same scan asserts that no file here reads a clock, a random source,
//     a file or the network, which is why every value the pipeline needs is
//     injected.
//
// The two decisions that shape the interfaces are P3.4's own dependencies, and
// both are answered: **D122** (the microphone is an OS permission with a
// usage-description string the owner writes; the app degrades to a visible
// "tap to speak" control when the OS refuses it -- `AUDIO_IN_CODES`) and
// **D123** (D46 inherited: on-device engines only, chosen by a bake-off
// measured on the oldest supported device -- owner-run check 85, phase 5).
//
// What is *not* here, and is on purpose: the mailbox client that signs and
// writes (`mobile/src/mailbox/`, P3.6), the voice screen and the interrupt
// switch (`mobile/src/voice/`, P3.5), the prompt queue (P6.5) and the spoken
// confirmation (P6.2, P6.12). Each of them is written against these ports.

export { AUDIO_IN_CODES, AUDIO_IN_EVENTS, AUDIO_IN_EVENT_FIELDS, FakeAudioIn, isAudioIn } from './audio-in.js';

export { STT_CODES, STT_EVENTS, STT_EVENT_FIELDS, FakeStt, isSpeechToText } from './stt.js';

export { TTS_CODES, FakeTts, isTextToSpeech } from './tts.js';

export { DRAFT_FIELDS, messageDraft } from './message.js';

export { PIPELINE_EVENTS, PIPELINE_EVENT_FIELDS, createVoicePipeline, isVoicePipeline } from './pipeline.js';

export { isRefusal } from './refusal.js';
