// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// Detail goes to the screen (mobile identity and pairing plan, Task X2; the
// spec's "Detail goes to the screen").
//
// The spec's sentence: *"In voice mode, when output is too dense to follow by
// ear (lists of PR ids, job tables), the agent says it has put the details on
// screen and shows them in the text view."* This file is both halves of it --
// the sentence the voice says, and the detail the text view draws -- and it is
// one file because the two are one decision: whether a reply is dense.
//
// **Two shapes are dense, and they are the two the spec names.** A **list of
// ids** (run ids like `20260926-221530-a1b2`, or pull-request ids like
// `#1234`), and a **table** (the pipe table an agent writes, with its header,
// its `---` rule and its rows). Everything else is read out loud. The rule is
// deliberately narrow, and the reason is which way a mistake costs: a reply
// wrongly called dense is *hidden* from an owner who is listening -- they hear
// "the details are on screen" and must look -- while a reply wrongly read out
// is merely tedious. So a list counts only when it is **nothing but** ids (one
// prose line and it is a sentence again), and a table only when the rule row is
// really there. A long paragraph is read out: the spec's examples are a list
// and a table, and a module that guessed a third shape would be guessing.
//
// **Where the detail goes, and why that is the text view.** The one
// conversation voice and text share is X1's transcript, and the text view draws
// it; so the detail is a line in it, the agent's own, entering through the
// outbox -- the only door a reply comes through (D2788). Nothing here draws
// anything: the view already draws the transcript, and a second surface for
// "the detail" would be a second list that could disagree with the first.
//
// **The port is the client's own speaker.** `transcriptSpeaker` (X1) wraps the
// port the mailbox client speaks replies through; `detailOnScreen` is that same
// port with the dense case handled -- a drop-in for it, so the app mounts one
// speaker on its client and the mode switch changes nothing about it (the
// client is built once, and its port with it). What it adds is exactly this:
// when a reply is dense, the voice says `DETAIL_SPEECH` instead of reading the
// table, and the table itself is appended to the transcript for the text view.
// The sentence the voice said is still a line -- it is what the agent said --
// and the detail is the line after it, which is the order it happened in: the
// agent announced, then showed.
//
// **A reply that was not spoken shows nothing.** The client leaves a reply it
// could not speak unacked and says it again at the next poll (X1's rule), so a
// detail appended for one that was not spoken would be the same table twice,
// once for a sentence nobody heard. So the append is on the speaker's `ok`, and
// on nothing else -- and the app's own refusal sentence (S6.3), which the
// client says over this same port, is not a dense reply and is not classified.

import { TRANSCRIPT_SOURCE, TRANSCRIPT_WHO, isTranscript, transcriptSpeaker } from './transcript.js';

/** The two shapes too dense to follow by ear. A closed set: a reply is dense in
 * one of these ways or it is read out. */
export const DETAIL_KINDS = Object.freeze({
  /** Nothing but a list of ids: run ids, pull-request ids, or both. */
  IDS: 'ids',
  /** A pipe table: a header row, a `---` rule, and rows. */
  TABLE: 'table',
});

/** What the voice says instead of reading the detail out (the spec's "says it
 * has put the details on screen"). One sentence, and the same one every time --
 * a caller that could reword it would be a second place the promise is made. */
export const DETAIL_SPEECH = 'The details are on screen.';

/** A run id, exactly: the form `fugaro runs ls` prints and a branch names
 * (`internal/task`'s `ValidRunID`, `20260926-221530-a1b2`). */
const RUN_ID_RE = /^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$/;

/** A pull-request id: the `#1234` an agent writes when it lists PRs. The `#` is
 * required -- a bare number line is not an id, it is a number. */
const PR_ID_RE = /^#[0-9]+$/;

/** A markdown list marker, which an agent puts before each id: `- `, `* `,
 * `1. `, `1) `. Stripped before the id is read, so a bulleted list of ids is
 * still a list of ids. */
const LIST_MARKER_RE = /^(?:[-*+]|[0-9]+[.)])\s+/;

/** Whether one line is an id and nothing else. */
function isIdLine(line) {
  const bare = line.trim().replace(LIST_MARKER_RE, '').trim();
  return RUN_ID_RE.test(bare) || PR_ID_RE.test(bare);
}

/** Whether one line is a table's rule row: cells of `-`, `:` and spaces, at
 * least one pipe and at least one dash. A bare `---` (a horizontal rule) has no
 * pipe and is not one. */
function isRuleRow(line) {
  const trimmed = line.trim();
  return trimmed.includes('|') && trimmed.includes('-') && /^[|:\- ]+$/.test(trimmed);
}

/** Whether one line has a cell in it (a pipe), which is what makes it a row
 * rather than a paragraph. */
function hasCells(line) {
  return line.trim().includes('|');
}

/**
 * How a reply is dense, or `null` for a reply to read out loud.
 *
 * `DETAIL_KINDS.IDS` when every non-empty line is an id (two or more of them --
 * one id is a fact, not a list), and `DETAIL_KINDS.TABLE` when a rule row sits
 * between two rows. Anything else, including a value that is not text, is
 * `null`: a reply this module does not recognise is a reply it does not hide.
 *
 * @param {unknown} text the reply
 * @returns {string|null} one of `DETAIL_KINDS`' values, or `null`
 */
export function detailKind(text) {
  if (typeof text !== 'string' || text === '') return null;

  const lines = text.split('\n');
  const spoken = lines.filter((one) => one.trim() !== '');
  if (spoken.length >= 2 && spoken.every(isIdLine)) return DETAIL_KINDS.IDS;

  for (let i = 1; i < lines.length - 1; i += 1) {
    if (isRuleRow(lines[i]) && hasCells(lines[i - 1]) && hasCells(lines[i + 1])) {
      return DETAIL_KINDS.TABLE;
    }
  }
  return null;
}

/**
 * The mailbox client's speaker port, with dense replies put on the screen.
 *
 * A drop-in for X1's `transcriptSpeaker` -- same arguments, same answer -- so
 * the app mounts one of the two on its client. An ordinary reply is that port's
 * behaviour unchanged (spoken, and recorded as the agent's line). A dense reply
 * is spoken as `DETAIL_SPEECH` and the detail itself is appended to the
 * transcript, which is the list the text view draws.
 *
 * @param {object} deps
 * @param {object} deps.transcript a transcript (`createTranscript`, X1)
 * @param {object} deps.speaker the port the client speaks through
 *   (`{ speak(text, speech?) }`)
 * @returns {object} the port the app hands `createMailboxClient`
 * @throws {TypeError} for a transcript or a speaker that is not one
 */
export function detailOnScreen({ transcript, speaker } = {}) {
  if (!isTranscript(transcript)) throw new TypeError('detailOnScreen: a transcript is required');
  if (speaker === null || typeof speaker !== 'object' || typeof speaker.speak !== 'function') {
    throw new TypeError('detailOnScreen: a speaker port (with speak()) is required');
  }

  // The recorder is X1's own port: what was said is a line because it was said,
  // and this file adds no second rule about that. The app's refusal sentence
  // (S6.3) is not classified and not appended, because it is not a reply.
  const recorder = transcriptSpeaker({ transcript, speaker });

  return Object.freeze({
    /**
     * Speak one reply, and put a dense one on the screen instead of reading it.
     *
     * @param {string} text what to say
     * @param {object} [speech] the client's word on what it is saying (S6.3):
     *   `{ refusal: true }` for the app's own refusal sentence
     * @returns {Promise<object>} the wrapped port's own verdict, unchanged
     */
    async speak(text, speech) {
      const isRefusal = speech !== null && typeof speech === 'object' && speech.refusal === true;
      if (isRefusal || detailKind(text) === null) return recorder.speak(text, speech);

      const said = await recorder.speak(DETAIL_SPEECH, speech);
      // Only a reply the owner actually heard is shown: a speak that failed
      // leaves the message unacked and the next poll says it again (X1), so a
      // detail appended here would be the same table twice.
      if (said !== null && typeof said === 'object' && said.ok === true) {
        transcript.append({ who: TRANSCRIPT_WHO.AGENT, source: TRANSCRIPT_SOURCE.OUTBOX, text });
      }
      return said;
    },
  });
}
