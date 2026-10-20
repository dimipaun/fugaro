// The seam the mailbox client speaks through, now that there is an audio tree
// to speak through (mobile plan, Task P3.5; Global Constraint 10, Voice D52).
//
// **What was stubbed, and what this file is.** P3.6's client has to speak the
// consumer's replies and to know whether the speaking happened -- its own
// verification is that it *acks a message only after it has spoken it*, which
// is a statement about a failed speak as much as a successful one. P3.6 was
// written before P3.4's audio interfaces were on `main`, so it defined the
// smallest seam its rule needs (`src/mailbox/speaker.js`: `speak(text)` in,
// a verdict out) and left the wiring to this task. `mobile/README.md` said so
// in as many words -- *`speaker.js` stays until the voice screen (P3.5) wires
// the mailbox client to the audio pipeline* -- and this is that wiring, in the
// module the plan's Files line gives P3.5 (`mobile/src/voice/`).
//
// **The two ports are not merged into one, and that is deliberate.** The
// mailbox module keeps its own `MemorySpeaker`, which is the double its tests
// are written against and which this module does not own; what arrives here is
// the **adapter** that makes P3.4's pipeline answer P3.6's port, so the client
// is handed one object and never learns what is behind it. A screen that built
// a client with a different speaker would be a second way to speak; the door's
// export list is what makes that an edit rather than an accident.
//
// **The mapping is the whole rule, and it is lossy on purpose.** The client
// asks one question -- *did the owner hear it?* -- so everything that is not a
// finished utterance is `speak-failed`, whatever the audio layer called it:
// a barge-in (`interrupted`), an utterance already in flight
// (`already-speaking`), a voice the engine would not start. Only `bad-text` is
// carried through as itself, because "there was nothing to say" is the one
// refusal the client's caller can fix. Nothing here reads a clock, a device or
// a network, and nothing is logged: an utterance is text, and text is what the
// mailbox carries (D118).

import { TTS_CODES, isVoicePipeline } from '../audio/index.js';
import { SPEAK_CODES } from '../mailbox/index.js';

/**
 * Wrap an audio pipeline as the mailbox client's speaker port.
 *
 * @param {object} deps
 * @param {object} deps.pipeline the pipeline (`isVoicePipeline`), built by
 *   `mobile/index.js` from the device's own audio ports
 * @returns {{ speak: (text: string) => Promise<{ok: true} | {ok: false, code: string, field: string}> }}
 * @throws {TypeError} when the pipeline is not one -- a wiring bug, at the
 *   point the app is built rather than at the first reply
 */
export function createPipelineSpeaker({ pipeline } = {}) {
  if (!isVoicePipeline(pipeline)) throw new TypeError('pipeline is a VoicePipeline');

  return Object.freeze({
    /**
     * Speak one reply, and answer whether the owner heard it.
     *
     * @param {string} text what to say
     * @returns {Promise<{ ok: true } | { ok: false, code: string, field: string }>}
     */
    async speak(text) {
      const said = await pipeline.say(text);
      if (said.ok) return Object.freeze({ ok: true });
      // The one refusal that is the caller's to fix is carried through; every
      // other way an utterance can fail to play is the same answer to the
      // client, which is "not spoken" -- and the message stays unacked, so the
      // next pass tries again.
      const code = said.code === TTS_CODES.BAD_TEXT ? SPEAK_CODES.BAD_TEXT : SPEAK_CODES.SPEAK_FAILED;
      return Object.freeze({ ok: false, code, field: 'text' });
    },
  });
}
