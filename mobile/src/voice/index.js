// The voice screen's one door (mobile plan, Task P3.5; D122, D127, D126).
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
// What is *not* here, and is on purpose: the spoken confirmation (**P6.12**,
// which builds on this task later -- the seam is `accept` and the pipeline's
// events, and none of it is implemented), the prompt queue and its wider
// interrupt table (**P6.5**, which is not a dependency of this task: see
// `interrupt.js` on the one place the two disagree), and the job list (P3.3).
// Each of them is written against this door.

export { QUEUE_CAP, SCREEN_CODES, createVoiceScreen } from './screen.js';

export { INTERRUPT_REASON, INTERRUPT_SWITCH, interruptDecision } from './interrupt.js';

export { createPipelineSpeaker } from './speaker.js';
