// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// One transcript for voice and text (mobile identity and pairing plan, Task X1;
// D2788). The spec's sentence is *"one session, two modes"* -- voice and text
// are two views of the same conversation with the agent -- and this file is the
// conversation.
//
// **What it is.** An append-only list of lines, in the order they were said,
// owned by no view: the voice screen writes the owner's spoken words into it,
// the text view writes the owner's typed ones, and both draw it. A view can be
// built, torn down and built again without the conversation losing a line,
// which is what "switching loses nothing" means on this side -- the transcript
// is the state that outlives the switch.
//
// **What a line is.** `{ id, at, who, source, text, engine }`, closed and
// checked fail-closed, the way `shared/mobile-session/port.js` checks a
// session: `who` is `operator` or `agent`, `source` is where the line entered
// the app (`voice`, `text` or `outbox` -- the mailbox's outbox is the only door
// a reply comes through, D2788), `engine` is the tag of the engine that
// produced a spoken line (P5.3.7, `null` for a typed one), and `id`/`at` are
// the caller's id source and clock (D117's server-anchored now). An unknown
// field is refused rather than ignored, for the port's reason: a value smuggled
// past this check is the one thing it exists to stop.
//
// **What is deliberately NOT here.** No client, no pipeline, no store and no
// writer: this file records what a view and the mailbox handed it and writes
// nothing anywhere. The mailbox client stays the app's one write path (D113,
// `src/mailbox/write-path.test.js`), and `recordingMailbox` below only *watches*
// a send that client already made. The session bridge is not here either: the
// transcript is handed a port, reads its `project_id` and subscribes to
// `signedOut`, and does not touch a token, a signature or a refresh -- that
// bridge is the voice and mailbox design's (J4, J5b).
//
// **Who writes which line, and why in this shape.**
//
//   * the operator's **spoken** words: `src/voice/screen.js`, at its own `accept`,
//     because the engine tag is a fact only the voice view holds at that moment;
//   * the operator's **typed** words: `recordingMailbox` below, because the typed
//     door is `src/text/input.js`'s `client.send` and the text view reaches the
//     transcript by handing that door a mailbox that records -- this task may not
//     edit `input.js`, and a wrapper that delegates the write is not a second
//     write path;
//   * the agent's **reply**: `transcriptSpeaker` below, wrapped around the
//     mailbox client's speaker port, so a reply spoken out of the outbox is the
//     reply the transcript holds. One read, one path (D2788). That port is the
//     app's own voice as well as the agent's: the client says its refusal
//     sentences through the same `speak` (S6.3), marks them as what they are,
//     and `transcriptSpeaker` records none of them -- a refusal is the app
//     telling the owner why nothing was sent, never the agent's words.
//
// **A refused line is not a line.** The transcript holds what reached the
// session with the operator's session: an utterance the mailbox refused was not
// said to anybody, and the text view's `unsent` list (D167's own half) is where
// refused text stays visible. So a send that refused appends nothing, and a
// reply the speaker could not speak appends nothing -- which is also the
// client's own rule ("never ack what was not spoken"): the next poll says it
// again, and a line recorded for it would double the sentence.
//
// **The conversation ends with the session.** Given a C2 port, the transcript
// carries the project's opaque id and stops at `signedOut` (M3's rotated-out
// refresh, D2794): a later line is refused `signed-out` rather than appended to
// a conversation whose session is over. What was already said stays -- the
// conversation happened -- and there is no "signed back in" on one transcript,
// because a new session is a new port value and a new transcript with it.

import { SESSION_EVENTS, assertSessionPort } from '../../../shared/mobile-session/port.js';

/** The refusal codes, one per class of fault, each with a test that names it. */
export const TRANSCRIPT_CODES = Object.freeze({
  /** The value handed to `append` is not a plain object at all. */
  BAD_LINE: 'bad-line',
  /** A line carrying a field the transcript does not name. Fail closed. */
  UNKNOWN_FIELD: 'unknown-field',
  /** A named field present as a value the transcript cannot read. */
  BAD_FIELD: 'bad-field',
  /** The clock is not a time, so there is no `at` to put on the line. */
  BAD_CLOCK: 'bad-clock',
  /** The id source answered something that is not an id. */
  BAD_ID: 'bad-id',
  /** A subscriber that is not a listener. */
  BAD_LISTENER: 'bad-listener',
  /** A line offered after the session's `signedOut`: the conversation is over. */
  SIGNED_OUT: 'signed-out',
});

/** Who said it. `operator` is the person; `agent` is the session on the other end. */
export const TRANSCRIPT_WHO = Object.freeze({
  OPERATOR: 'operator',
  AGENT: 'agent',
});

/**
 * Where the line entered the app. The three are the three doors a line comes
 * through and nothing else: the voice screen, the typed door, and the mailbox's
 * outbox -- which is the only door a reply comes through (D2788).
 */
export const TRANSCRIPT_SOURCE = Object.freeze({
  VOICE: 'voice',
  TEXT: 'text',
  OUTBOX: 'outbox',
});

/** The fields a line has, exactly. A member outside this list is refused. */
const LINE_FIELDS = Object.freeze(['who', 'source', 'text', 'engine']);

const WHO_VALUES = Object.freeze(Object.values(TRANSCRIPT_WHO));
const SOURCE_VALUES = Object.freeze(Object.values(TRANSCRIPT_SOURCE));

// The transcripts this module built, so `transcriptSpeaker` and
// `recordingMailbox` refuse a value that is not one where the app is wired
// rather than at the first line. A brand and not a shape test: the store is
// frozen and its methods are closures, so there is nothing else to recognise it
// by -- and `isTranscript` is exported for a caller that wants the same answer
// without trying it.
const TRANSCRIPTS = new WeakSet();

function refuse(code, field = null) {
  return Object.freeze({ ok: false, code, field });
}

function isPlainObject(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

/**
 * Whether `value` is a transcript this module built.
 *
 * @param {unknown} value
 * @returns {boolean}
 */
export function isTranscript(value) {
  return TRANSCRIPTS.has(value);
}

/**
 * Build the one transcript.
 *
 * `now` and `newId` are the caller's, like every other id and clock in this app
 * (`src/mailbox`'s client takes the same two); they are required and a bad one
 * throws, because a store that could not stamp a line is one that would fail at
 * the first sentence. `session` is optional: given C2's port -- mounted with
 * `assertSessionPort`, which is the caller's own step -- the transcript records
 * the project's opaque id (a display claim, never a token) and stops at
 * `signedOut`.
 *
 * @param {object} [options]
 * @param {object|null} [options.session] the project's session (C2's port)
 * @param {() => number} options.now D117's server-anchored now, in ms
 * @param {() => string} options.newId the id source the transcript mints from
 * @returns {object} the transcript: `project_id`, `append`, `entries`,
 *   `subscribe`, `ended`
 * @throws {TypeError} for a clock or an id source that cannot be one
 * @throws {SessionPortError} for a session that is not one (the port's own
 *   refusal: this module does not carry a second vocabulary for it)
 */
export function createTranscript(options = {}) {
  if (!isPlainObject(options)) throw new TypeError('createTranscript: an options object is required');
  const { session = null, now, newId } = options;
  if (typeof now !== 'function') throw new TypeError('createTranscript: a clock is required');
  if (typeof newId !== 'function') throw new TypeError('createTranscript: an id source is required');

  /** The lines, oldest first. Nothing here is capped: a lost line is the one
   * thing this store may not do ("switching loses nothing"). */
  const lines = [];
  /** The subscribers, in the order they subscribed. */
  const listeners = new Set();
  /** The project this conversation is with, or `null` -- never a token. */
  let projectId = null;
  /** Whether the session's `signedOut` has fired. */
  let ended = false;

  if (session !== null && session !== undefined) {
    const mounted = assertSessionPort(session);
    projectId = mounted.project_id;
    // The subscription is kept for the life of the transcript and is never
    // dropped: the port fires `signedOut` once, and a transcript that
    // unsubscribed would have to decide when the conversation is over -- which
    // the session has just answered.
    mounted.on(SESSION_EVENTS.SIGNED_OUT, () => {
      ended = true;
    });
  }

  /** Tell every subscriber, one at a time. See the note on a throwing listener. */
  function announce(line) {
    for (const listener of [...listeners]) {
      try {
        listener(line);
      } catch {
        // A subscriber that threw is not this store's failure, and the app has
        // no logger to hand it to (`boundary.test.js` rule 4). The other
        // subscribers still hear the line and the append still answers, which
        // is what keeps one view's fault from stopping the conversation.
      }
    }
  }

  /**
   * Add one line, in the order it was said.
   *
   * The line's `id` and `at` are this store's (from `newId` and `now`); `who`,
   * `source`, `text` and `engine` are the caller's. A line whose `engine` is
   * absent is a line no engine produced -- a typed one -- and its `engine` is
   * stored as `null` rather than left off, so every line has the same six
   * fields.
   *
   * @param {unknown} line `{ who, source, text, engine? }`
   * @returns {{ok: true, line: object} | {ok: false, code: string, field: string|null}}
   */
  function append(line) {
    if (ended) return refuse(TRANSCRIPT_CODES.SIGNED_OUT, 'session');
    if (!isPlainObject(line)) return refuse(TRANSCRIPT_CODES.BAD_LINE, null);
    for (const key of Object.keys(line)) {
      if (!LINE_FIELDS.includes(key)) return refuse(TRANSCRIPT_CODES.UNKNOWN_FIELD, key);
    }
    if (!WHO_VALUES.includes(line.who)) return refuse(TRANSCRIPT_CODES.BAD_FIELD, 'who');
    if (!SOURCE_VALUES.includes(line.source)) return refuse(TRANSCRIPT_CODES.BAD_FIELD, 'source');
    if (typeof line.text !== 'string' || line.text === '') return refuse(TRANSCRIPT_CODES.BAD_FIELD, 'text');
    const engine = line.engine === undefined ? null : line.engine;
    if (engine !== null && (typeof engine !== 'string' || engine === '')) {
      return refuse(TRANSCRIPT_CODES.BAD_FIELD, 'engine');
    }

    const at = now();
    if (!Number.isSafeInteger(at)) return refuse(TRANSCRIPT_CODES.BAD_CLOCK, 'now');
    const id = newId();
    if (typeof id !== 'string' || id === '') return refuse(TRANSCRIPT_CODES.BAD_ID, 'id');

    const entry = Object.freeze({ id, at, who: line.who, source: line.source, text: line.text, engine });
    lines.push(entry);
    announce(entry);
    return Object.freeze({ ok: true, line: entry });
  }

  /**
   * The conversation, oldest first, as a frozen list. A copy: a caller cannot
   * append to the transcript by pushing on what it was handed.
   */
  function entries() {
    return Object.freeze([...lines]);
  }

  /**
   * Hear every line appended from now on. The listener is handed the frozen
   * line; the answer is the function that unsubscribes, as C2's `on` answers.
   *
   * @param {unknown} listener
   * @returns {{ok: true, unsubscribe: () => void} | {ok: false, code: string, field: string|null}}
   */
  function subscribe(listener) {
    if (typeof listener !== 'function') return refuse(TRANSCRIPT_CODES.BAD_LISTENER, 'listener');
    listeners.add(listener);
    return Object.freeze({
      ok: true,
      unsubscribe: () => {
        listeners.delete(listener);
      },
    });
  }

  const transcript = Object.freeze({
    project_id: projectId,
    append,
    entries,
    subscribe,
    /** Whether the session's `signedOut` has fired: the conversation is over. */
    ended: () => ended,
  });
  TRANSCRIPTS.add(transcript);
  return transcript;
}

/**
 * The mailbox client's speaker port, with the transcript behind it.
 *
 * The client speaks every outbox message it read and verified (`src/mailbox`'s
 * `poll`), and answers only whether the owner heard it; this wrapper keeps that
 * answer exactly as it is and records the sentence as the agent's line when it
 * was spoken. That is the whole of "replies arrive through the mailbox outbox"
 * (D2788): the reply is not read a second time from anywhere, and there is no
 * second door into the transcript for the agent's words.
 *
 * **The app's own sentences are not lines.** The client speaks its refusals
 * through this same port (S6.3), and one of those -- "No connection. I have not
 * sent that." -- is the app telling the owner what it just did with their
 * words. It marks that speech (`{ refusal: true }`, the port's second
 * argument), and this wrapper records nothing for it: a refusal is the voice
 * and text views' own `refused` state, and a line for it would be the
 * transcript putting words in the agent's mouth. A port that says nothing about
 * the speech is saying a reply.
 *
 * A speak that did not happen records nothing either -- see the header: the
 * message stays unacked and the next poll says it again, so a line recorded
 * here would be the same sentence in the transcript twice.
 *
 * @param {object} deps
 * @param {object} deps.transcript a transcript (`createTranscript`)
 * @param {object} deps.speaker the port the client speaks through
 *   (`{ speak(text, speech?) }`, e.g. `src/voice/speaker.js`'s
 *   `createPipelineSpeaker`)
 * @returns {object} the port the app hands `createMailboxClient`
 * @throws {TypeError} for a transcript or a speaker that is not one
 */
export function transcriptSpeaker({ transcript, speaker } = {}) {
  if (!isTranscript(transcript)) throw new TypeError('transcriptSpeaker: a transcript is required');
  if (speaker === null || typeof speaker !== 'object' || typeof speaker.speak !== 'function') {
    throw new TypeError('transcriptSpeaker: a speaker port (with speak()) is required');
  }

  return Object.freeze({
    /**
     * Speak one reply and record it as the agent's line, if it was spoken and
     * it was the agent's.
     *
     * @param {string} text what to say
     * @param {object} [speech] the client's word on what it is saying (S6.3):
     *   `{ refusal: true }` for the app's own refusal sentence
     * @returns {Promise<object>} the wrapped port's own verdict, unchanged
     */
    async speak(text, speech) {
      const said = await speaker.speak(text, speech);
      const isRefusal = speech !== null && typeof speech === 'object' && speech.refusal === true;
      if (said !== null && typeof said === 'object' && said.ok === true && !isRefusal) {
        // The line the store refused is not this port's answer to give: the
        // client asks one question ("did the owner hear it?") and a refusal
        // here would be a reply the client would not ack over a fault that is
        // the transcript's to show, not the mailbox's to retry.
        transcript.append({ who: TRANSCRIPT_WHO.AGENT, source: TRANSCRIPT_SOURCE.OUTBOX, text });
      }
      return said;
    },
  });
}

/**
 * A mailbox client with the transcript behind its `send`.
 *
 * The text view's typed words reach the mailbox through `client.send`
 * (`src/text/input.js`), which is the app's one write path; this wrapper is how
 * that view reaches the transcript without a second write: it calls the client
 * it was given, records the line the client **accepted**, and answers the
 * client's own verdict unchanged. Every other method is carried over as it was,
 * so a screen handed this object has the whole client.
 *
 * A send the client refused -- offline, lapsed, a full queue -- records
 * nothing, and refuses nothing of its own: the words never reached the session,
 * and `unsent()` (D167) is where refused text is kept for the owner to see.
 *
 * A send the client accepted records **the text that was signed**, which is the
 * redacted one (D118): the client hands its own redaction back as `sent.text`,
 * and the line is that string rather than the words this wrapper was given, so
 * a secret is in neither the message nor the conversation.
 *
 * @param {object} deps
 * @param {object} deps.transcript a transcript (`createTranscript`)
 * @param {object} deps.mailbox the mailbox client (`{ send(text, options) }`)
 * @param {string} [deps.source] which door the typed words came through:
 *   `TRANSCRIPT_SOURCE.TEXT` (the default) or `.VOICE`. `.OUTBOX` is refused --
 *   the agent's words are `transcriptSpeaker`'s and never a send's.
 * @returns {object} the mailbox, with `send` recording what it accepted
 * @throws {TypeError} for a transcript, a mailbox or a source that is not one
 */
export function recordingMailbox({ transcript, mailbox, source = TRANSCRIPT_SOURCE.TEXT } = {}) {
  if (!isTranscript(transcript)) throw new TypeError('recordingMailbox: a transcript is required');
  if (mailbox === null || typeof mailbox !== 'object' || typeof mailbox.send !== 'function') {
    throw new TypeError('recordingMailbox: the mailbox client (with send()) is required');
  }
  if (source !== TRANSCRIPT_SOURCE.TEXT && source !== TRANSCRIPT_SOURCE.VOICE) {
    throw new TypeError('recordingMailbox: the source is the text or the voice door');
  }

  return Object.freeze({
    ...mailbox,
    async send(text, options) {
      const sent = await mailbox.send(text, options);
      // The line is the REDACTED text the client signed (`sent.text`), not the
      // words handed in: D118's secrets are the client's rule, the record on the
      // wire holds `[REDACTED]`, and a conversation that drew the raw string
      // would put a secret on a screen and disagree with the message that
      // reached the session. A verdict that carries no text is a client that is
      // not this one: nothing is guessed, and no line is written.
      if (sent !== null && typeof sent === 'object' && sent.ok === true && typeof sent.text === 'string') {
        transcript.append({ who: TRANSCRIPT_WHO.OPERATOR, source, text: sent.text });
      }
      return sent;
    },
  });
}
