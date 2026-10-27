// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The voice screen's one door (mobile plan, Tasks P3.5 and P6.12; D122, D127,
// D126, D162, D164, D168, D169).
//
// A screen imports `src/voice/index.js` and gets the screen, the switch it is
// gated by, and the adapter that puts the audio pipeline behind the mailbox
// client's speaker port. `src/audio/index.js` and `src/mailbox/index.js` are
// the precedents, and the reason holds here: a file that imports `screen.js`
// directly can build a second screen, and this module's own test pins this
// door's exports exactly.
//
// **What P3.5 ships, and what it deliberately does not.** The plan's Files line
// is `mobile/src/voice/` and its verification is four sentences, all of them in
// `screen.test.js` and `interrupt.test.js`. There is **no speech engine and no
// speech recognition here** -- D123 inherits Voice D46 (on-device engines only,
// chosen by a bake-off measured on the oldest supported device: owner-run check
// **85**), and the voiceless first store build (the owner's ruling of
// 2026-10-19) means this is the screen's *logic*, written and tested against
// P3.4's interfaces and fakes. Nothing here reaches a device, a network, a
// database or a clock of its own, and the module's scan is what keeps that
// true rather than promised.
//
// **P6.12 shipped the spoken confirmation through the seam P3.5 left.**
// `dialogue.js` is where a final transcript is routed (P6.1), where a
// destructive command's read-back is spoken and waited on (P6.2, D169) and
// where P6.5's prompt pause is decided -- and the screen takes it as an
// optional `utterance` delegate, so the words it routes as agent text still go
// through the screen's own `accept` and its offline refusal. What is *not* here,
// and is on purpose: the prompt queue itself and its wider interrupt table
// (**P6.5**, a pure module in `shared/`), and the job list (P3.3).

export { QUEUE_CAP, SCREEN_CODES, createVoiceScreen } from './screen.js';

export { INTERRUPT_REASON, INTERRUPT_SWITCH, interruptDecision } from './interrupt.js';

export { createPipelineSpeaker } from './speaker.js';

export { DIALOGUE_CODES, DIALOGUE_OUTCOME, createVoiceDialogue } from './dialogue.js';
