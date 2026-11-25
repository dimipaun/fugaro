// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The text screen's one door (mobile plan, Task P6.9; D167; identity and
// pairing plan, Task X1; D2788).
//
// A screen imports `src/text/index.js`. It builds the typed pipeline with
// `createTypedInput` over the mailbox client it already has (P3.6's
// `createMailboxClient`, from `src/mailbox/`) and draws `createTextScreen` with
// react-native's primitives, exactly as `src/app/root.js` is drawn. Everything
// else here is the vocabulary those two answer in.
//
// **The one transcript is here too** (X1, D2788). The app builds it once with
// `createTranscript` -- over C2's session port, if it has one -- and hands it to
// both views: the voice screen records the owner's spoken words in it, the text
// screen draws it through its `conversation` prop, and the mailbox's outbox
// reaches it through `transcriptSpeaker`, wrapped around the client's speaker
// port. `recordingMailbox` is the fourth seam, for a typed line: it is the
// mailbox client with `send` recording what the client accepted, so the typed
// door (`input.js`, which this task does not own) needs no change and the client
// stays the app's one write path.
//
// **The draft and the detail are here too** (X2). `composer.js` holds the one
// string the owner has typed or pasted and not sent -- outside the view, so a
// switch to the voice screen and back or a rotation does not lose it -- and
// `detail.js` is the other half of the text interface's spec sentence: a reply
// too dense to follow by ear (a list of ids, a table) is announced with one
// sentence and put on the screen, as a line of the same transcript the text
// view already draws. Neither writes anything.
//
// **What is deliberately NOT exported here: anything that writes.** There is no
// store, no paths and no client in this module's exports: the mail is the
// caller's client, and this screen is handed it rather than building one. The
// transcript writes nothing either -- it holds lines a view or the client
// produced -- and `src/mailbox/`'s `write-path.test.js` holds that fence. The
// fastest way to break it would be a screen with a second way to reach a
// database.
//
// **What is not here yet, and is named in the pull request.** A confirmed
// destructive command is authorised by P6.2's machine and its record is
// returned, not delivered: nothing in `src/text/` calls the client's action
// write. That write exists -- `client.send(text, { action })`, added with P6.10,
// writes P6.3's action record and never queues it -- so what is missing is the
// wiring, which belongs to the screens that own a confirm path (this task's
// remaining half, P6.9, and P6.12 for the voice one). See `input.js`'s header
// for the whole of it, including what the screens would have to decide.

export {
  INPUT_CODES,
  INPUT_OUTCOME,
  TYPE_CUE,
  createTypedInput,
  typedReadBack,
} from './input.js';

export { COMPOSER_CODES, LONG_PASTE_CHARS, createComposer } from './composer.js';

export { DETAIL_KINDS, DETAIL_SPEECH, detailKind, detailOnScreen } from './detail.js';

export {
  CONVERSATION_LABEL,
  CONVERSATION_LIMIT,
  DESTRUCTIVE_WORD,
  INPUT_HINT,
  NO_CONVERSATION,
  NO_PROMPTS,
  NO_SUMMARIES,
  RECENT_LABEL,
  RECENT_SUMMARY_LIMIT,
  SCREEN,
  SPEAKER_WORD,
  TEXT_TITLE,
  conversationLines,
  createTextScreen,
  recentSummaries,
} from './screen.js';

export {
  TRANSCRIPT_CODES,
  TRANSCRIPT_SOURCE,
  TRANSCRIPT_WHO,
  createTranscript,
  isTranscript,
  recordingMailbox,
  transcriptSpeaker,
} from './transcript.js';
