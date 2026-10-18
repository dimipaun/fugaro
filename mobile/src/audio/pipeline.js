// The audio pipeline: the three ports wired into one voice turn (mobile plan,
// Task P3.4; D122, D123, D126, D118).
//
// **What this module is, and what it is not.** It is the *audio* half of the
// voice path and nothing else: the capture path's events drive a turn through
// the transcription port (`transcript` + `message`), the output port speaks a
// reply and reports how it ended (`utterance`), and a speech event arriving
// while the assistant is speaking cancels it (the barge-in §5 asks for). What
// it is **not** is the voice screen or the controller: the interrupt switch
// (D127), the offline state (P1.4), the queue of proactive updates (P6.5), the
// spoken confirmation (P6.2, P6.12) and the mailbox client that signs and
// writes (P3.6) are all later tasks, and each of them is written against these
// ports rather than against this file. Keeping the split there is what makes
// P3.5's logic testable with the fakes alone (D126, Global Constraint 10).
//
// **The plan's three verification sentences, as rules.**
//
//   1. *a fake transcript becomes a message* -- a `final` transcript arriving
//      while a turn is open is composed by `message.js` and handed to the
//      shared schema; the schema's verdict is what is emitted, so a transcript
//      it refuses (the cough that produced the empty string) produces a
//      `refused` event and no message at all.
//   2. *a fake reply becomes an utterance* -- `say(text)` hands one utterance
//      to the output port and reports its outcome when the port answers:
//      `finished` when it played out, `interrupted` when it did not, and a
//      `refused` event when the port would not start it (empty text, or a
//      second utterance while one is playing).
//   3. *an interrupted utterance is reported as interrupted* -- any speech
//      event during playback cancels it. Both of the capture path's two ways
//      of saying that are honoured: `barge-in` (the signal a path with echo
//      cancellation produces) and a plain `speech-start` (which is all a
//      simpler path can say). Neither is optional: §5's barge-in is the
//      feature, and a device that could not be interrupted by speaking is a
//      device where the assistant talks over the user.
//
// **The three injected things, and why they are injected.** The message's
// `at`, its `id` and its `sender` are the only values here that are not the
// ports': the clock is the server-anchored one D117 insists on, and the id and
// sender are the app's identity, which P3.6 supplies. This module therefore
// reads **no clock**, **no random source**, **no file** and **no network** --
// which is what makes every one of its tests deterministic and what the
// module's own scan asserts (there is no `Date.now`, no `Math.random`, no
// `fetch` and no timer in any file under `src/audio/`).
//
// **A stopped or superseded turn produces no message.** Cases that look
// different and are the same rule: the engine's `final` arriving with no turn
// open (its flush racing a `stop()`), or for a turn this pipeline has left
// behind (its flush racing a cancel, or a new `speech-start`), is dropped -- and
// the turn is closed the moment one `final` is taken, so a second one cannot
// arrive for it. A message the user cancelled, or one that arrives after the
// session stopped, is a message sent that nobody asked for -- §5's grace period
// exists precisely so "cancel that" is possible, and this is the line that makes
// it true rather than aspirational.
//
// **The turn outlives the end of speech, and that is the whole reason this
// file has a state machine.** Speech ending is not the transcript arriving: the
// capture path says the user stopped, the engine then produces its `final` a
// moment later (`stt.js` calls that interval the flush). A pipeline that closed
// its turn on `speech-end` would drop every `final` -- every message, always --
// so the mirrored state is `idle` / `open` (the user is speaking) / `flushing`
// (speech ended, the engine owes a `final`), and only a `final`, a `stop()` or
// a new turn abandons the last two. The engine, not the microphone, decides
// when a transcript is finished; the microphone only decides when a turn
// starts.
//
// **The state alone cannot say whose words these are, so the turn id does.**
// Every event carries the id `begin()` answered with, and both handlers below
// compare it against the turn this pipeline believes it is in. The case that
// forces it -- and the one a state machine alone gets *wrong*, not merely
// incomplete -- is speech starting again while the engine is still flushing:
// that abandons the old turn and opens a new one, and if the abandoned turn's
// `final` arrives now, `state` is `open` and the words look like this turn's.
// Composing them would send the text the user moved on from **and** swallow the
// text they are actually saying, because the real `final` follows a moment
// later and the turn has already been closed. Both halves of that are the
// failure §5's grace period exists to prevent, so the rule is the id: an event
// whose id is not the current turn's is not this turn's, and is dropped.

import { isAudioIn } from './audio-in.js';
import { isSpeechToText } from './stt.js';
import { isTextToSpeech, TTS_CODES } from './tts.js';
import { messageDraft } from './message.js';

/** The events the pipeline emits. Nothing else is an event. */
export const PIPELINE_EVENTS = Object.freeze(['transcript', 'message', 'utterance', 'refused', 'unavailable']);

/**
 * The exact fields of each event, as a frozen map.
 *
 * This is the same rule the two ports carry one level down (D118, F26): the
 * pipeline says **what happened** -- words, a state, a refusal code -- and
 * never anything heard. A field added here would be a field a screen could
 * render and a log could hold, so the tests walk every event they produce and
 * assert its fields are exactly these.
 */
export const PIPELINE_EVENT_FIELDS = Object.freeze({
  /** `final` is false for a partial: what the screen shows while the user speaks. */
  transcript: Object.freeze(['kind', 'text', 'final']),
  /** A message the shared schema accepted, ready for P3.6 to sign and write. */
  message: Object.freeze(['kind', 'message']),
  /** `state` is `finished` or `interrupted`, and nothing else. */
  utterance: Object.freeze(['kind', 'state', 'text']),
  /** A value the pipeline would not accept, with the port's or the schema's code. */
  refused: Object.freeze(['kind', 'code', 'field']),
  /** D122: the OS refused the microphone or the background audio mode. */
  unavailable: Object.freeze(['kind', 'code']),
});

/**
 * Whether `value` is a pipeline: the six methods, as functions.
 *
 * @param {unknown} value a candidate pipeline
 * @returns {boolean}
 */
export function isVoicePipeline(value) {
  if (value === null || typeof value !== 'object') return false;
  return ['start', 'stop', 'say', 'speaking', 'turn', 'on'].every((name) => typeof value[name] === 'function');
}

/**
 * Wire a capture path, a transcription port and a speech output into one
 * pipeline.
 *
 * The three ports and the three injected values are all required, and a
 * missing or wrongly-shaped one **throws** rather than being reported later: a
 * `TypeError` at construction is the difference between a screen that never
 * works and a screen that silently never hears anything.
 *
 * @param {object} deps
 * @param {object} deps.audioIn the capture path (`isAudioIn`)
 * @param {object} deps.stt the transcription port (`isSpeechToText`)
 * @param {object} deps.tts the speech output port (`isTextToSpeech`)
 * @param {string} deps.sender the writing identity P3.6 signs with
 * @param {() => string} deps.newId a fresh id per message (P3.6 supplies the source)
 * @param {() => number} deps.now the server-anchored clock, in epoch ms (D117)
 * @returns {object} the pipeline
 */
export function createVoicePipeline({ audioIn, stt, tts, sender, newId, now } = {}) {
  if (!isAudioIn(audioIn)) throw new TypeError('audioIn is an AudioIn port');
  if (!isSpeechToText(stt)) throw new TypeError('stt is a SpeechToText port');
  if (!isTextToSpeech(tts)) throw new TypeError('tts is a TextToSpeech port');
  if (typeof sender !== 'string' || sender.length === 0) throw new TypeError('sender is a non-empty string');
  if (typeof newId !== 'function') throw new TypeError('newId is a function');
  if (typeof now !== 'function') throw new TypeError('now is a function');

  const handlers = new Map(PIPELINE_EVENTS.map((kind) => [kind, new Set()]));
  /** `idle`, `open` or `flushing` -- the engine's turn, mirrored here, because
   * the pipeline is what decides whether a `final` may become a message. */
  let state = 'idle';
  /** The id of the turn this pipeline is in, and **0** when it is in none. Every
   * event is compared against it: see the header on why the state alone cannot
   * say whose words arrived. */
  let turnId = 0;

  function emit(kind, fields) {
    const event = Object.freeze({ kind, ...fields });
    for (const handler of [...handlers.get(kind)]) handler(event);
  }

  /** Whether an event belongs to the turn this pipeline is in. `0` is never a
   * turn's id, so an engine that never opened one matches nothing. */
  function inTurn(event) {
    return state !== 'idle' && event.turnId === turnId;
  }

  /** Open a turn. A refusal from the engine opens nothing, so the mirror and
   * the engine cannot drift apart. */
  function beginTurn() {
    const begun = stt.begin();
    if (!begun.ok) {
      emit('refused', { code: begun.code, field: begun.field });
      return;
    }
    state = 'open';
    turnId = begun.turnId;
  }

  function onSpeechStart() {
    // Barge-in, both signals: speech while the assistant is speaking stops the
    // playback it is interrupting, and the utterance reports itself
    // `interrupted` when its promise settles. Then the new turn opens.
    if (tts.speaking()) tts.cancel();
    if (state === 'open') return; // the capture path saying it twice is one turn
    // `flushing` is the previous turn the engine still owes a `final` for, and
    // this is the user starting a new one: the old transcript is abandoned, not
    // raced. `turnId` is cleared with it, so the abandoned turn's `final` --
    // which may arrive while the new turn is already open -- is not this turn's
    // words.
    if (state === 'flushing') stt.cancel();
    state = 'idle';
    turnId = 0;
    beginTurn();
  }

  function endTurn() {
    // `speech-end` flushes; the engine's `final` is what ends the turn. See the
    // header: closing it here would drop every message. The turn id stays: the
    // engine still owes this turn a `final`.
    if (state !== 'open') return;
    const ended = stt.end();
    if (!ended.ok) {
      emit('refused', { code: ended.code, field: ended.field });
      return;
    }
    state = 'flushing';
  }

  function onPartial(event) {
    // A partial is what the user is still saying: it is shown, and it is never
    // a message. Only a `final` is. Nor is a partial from a turn this pipeline
    // is not in -- the abandoned turn's last revision, or one the engine sends
    // after the end of speech -- shown at all: the words on screen are the
    // words of the turn in progress.
    if (!inTurn(event) || state !== 'open') return;
    emit('transcript', { text: event.text, final: false });
  }

  function onFinal(event) {
    // An event from a turn this pipeline is not in is dropped here, before an
    // id is drawn: the abandoned turn's flush, and the flush that raced a stop.
    // Composing it would send text the user moved on from and swallow the text
    // they are saying, since it also closes the turn the real final belongs to.
    if (!inTurn(event)) return;
    state = 'idle';
    turnId = 0;
    const draft = messageDraft({ id: newId(), sender, text: event.text, at: now() });
    if (!draft.ok) {
      emit('refused', { code: draft.code, field: draft.field });
      return;
    }
    emit('transcript', { text: event.text, final: true });
    emit('message', { message: draft.message });
  }

  function reportUtterance(text, verdict) {
    if (verdict.ok) emit('utterance', { state: 'finished', text });
    else if (verdict.code === TTS_CODES.INTERRUPTED) emit('utterance', { state: 'interrupted', text });
    else emit('refused', { code: verdict.code, field: verdict.field });
  }

  audioIn.on('speech-start', onSpeechStart);
  audioIn.on('barge-in', onSpeechStart);
  audioIn.on('speech-end', endTurn);
  stt.on('partial', onPartial);
  stt.on('final', onFinal);

  return {
    /**
     * Start listening. Answers the capture path's own verdict -- `{ ok: true,
     * changed }`, or D122's `microphone-denied` / `background-audio-denied` --
     * and emits `unavailable` when the OS refused, which is what P3.5's
     * "tap to speak" control is built on. Starting twice is not an error.
     */
    async start() {
      const started = await audioIn.start();
      if (!started.ok) emit('unavailable', { code: started.code });
      return started;
    },

    /**
     * Stop listening: the microphone stops, an utterance in flight is
     * interrupted (and reports itself so), and any open turn is abandoned
     * without a final -- so the engine's flush after a stop cannot become a
     * message.
     */
    async stop() {
      if (tts.speaking()) tts.cancel();
      stt.cancel();
      state = 'idle';
      turnId = 0;
      return audioIn.stop();
    },

    /**
     * Speak one reply. Answers the output port's verdict verbatim:
     * `{ ok: true, spoken: true }`, or `{ ok: false, code: 'interrupted' }`
     * when the utterance was cancelled, or a `bad-text` / `already-speaking`
     * refusal when it never started. The `utterance` event is emitted when
     * that promise settles -- a microtask after a cancel -- and a refusal is
     * emitted as a `refused` event.
     *
     * @param {string} text what to say
     */
    say(text) {
      const spoken = tts.speak(text);
      spoken.then((verdict) => reportUtterance(text, verdict));
      return spoken;
    },

    /** Whether an utterance is in flight right now. */
    speaking() {
      return tts.speaking();
    },

    /**
     * Whether a turn is in flight: the user is being listened to, or the engine
     * is finishing the transcript of the turn that just ended. True from
     * `speech-start` until the `final` is taken -- so P3.5's screen can show
     * "listening" for as long as a transcript can still arrive, and a turn the
     * user cancelled or stopped reads false immediately.
     */
    turn() {
      return state !== 'idle';
    },

    /**
     * Subscribe to one event kind. Returns an idempotent unsubscribe; a bad
     * kind or a handler that is not a function throws (see `refusal.js`).
     */
    on(kind, handler) {
      if (!PIPELINE_EVENTS.includes(kind)) throw new TypeError(`unknown event: ${kind}`);
      if (typeof handler !== 'function') throw new TypeError('a handler is a function');
      handlers.get(kind).add(handler);
      return () => handlers.get(kind).delete(handler);
    },
  };
}
