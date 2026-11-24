// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The text screen's tree (mobile plan, Task P6.9; D167, D170, D114).
//
// D167's second half is one screen: "a text screen listing pending prompts and
// recent summaries per installation, accepting typed input and typed answers".
// This file draws it and does nothing else -- it has no clock, no client and no
// queue of its own, and every string it shows is a value it was handed. The
// typed input itself is `input.js`'s; the screen's job is to show what is
// waiting and to hand the keyboard's text back to the caller.
//
// **Why a function of React and react-native, like the shell.** Nothing here
// may `import 'react'` or `'react-native'`: the Expo toolchain is not installed
// in this environment and no test in this repository may need a device (Global
// Constraint 10). The primitives are arguments, exactly as they are to
// `createRoot`, and `screen.test.js` walks the tree this builds against a fake
// React. That is also how "the screen renders no secret" stays a fact in CI:
// the walk sees every string the tree holds.
//
// **Per installation, and never summed.** D170's rule -- one row per
// installation with its own freshness and no summed total -- is why
// `installations` is a list of sections rather than one flat list, and why
// nothing here counts across them. P6.5's `pendingAcross` already refuses to
// sum; a screen that added the counts back up would undo it. Within one
// installation the order is P6.5's own listing order, passed through unchanged
// (the oldest first), and the recent summaries are newest first under it.
//
// **What a section shows.** The installation's id (D114's label: the thing the
// owner typed so they can tell two apart, and the same string the shell
// already shows), the questions waiting as P6.5's listing gives them, and the
// recent summaries the app has -- the consumer's short lines about what the
// agent has done. A prompt's `destructive` flag is drawn as its own word rather
// than left to the sentence, because the whole point of the flag (P6.5) is that
// a destructive question must not be answered by accident.
//
// The listing entry's `stage` (P6.5's reminder escalation: never announced, an
// hour, a day) is **read by nothing here**, and this file says so rather than
// leaving the sentence above to imply it. Drawing a reminder stage is a
// decision about how a phone shows "you have been asked this for a day", and
// it is P6.12's spoken prompt and D170's overview that own how a reminder is
// presented; a word invented here would be one more spelling of a state this
// screen does not own. What the section does with the entry is: draw its
// sentence, and mark it when it is destructive.
//
// **The one conversation** (mobile identity and pairing plan, Task X1; D2788).
// The spec's sentence is *"one session, two modes"*: voice and text are two
// views of the same conversation with the operator's session, and the list of
// lines is `transcript.js`'s. This screen draws it, above the installation
// sections, from the `conversation` prop -- a value it is handed, like every
// other line on it (`transcript.entries()`, or any list shaped like one). It
// subscribes to nothing and holds nothing: a screen that kept the conversation
// would be a second copy of it, and the second copy is the one that loses a
// line when the owner switches views.
//
// The lines are drawn in the order the store holds them -- the order they were
// said -- and the most recent `CONVERSATION_LIMIT` of them: a conversation has
// no natural end and a phone has one screenful, so the cap is named here rather
// than left to whichever list the caller happens to pass. Unlike `recentSummaries`,
// this function does **not** sort: the transcript's order is chronological by
// construction, and a second ordering that disagreed with it by a millisecond
// would draw the reply above the words it answers.
//
// **What is not here.** No answer buttons: a typed answer goes through the
// caller's queue (`shared/prompts`' `answer`) and this screen is handed the
// keyboard's text, not the prompt it answers -- matching a typed line to a
// waiting question is the caller's, because the matching rule ("the active
// installation's oldest question", §3.4) is state this screen does not hold.
// No launch button either: a launch is D162's pass-through and belongs to the
// action it starts. And no write: a conversation line reaches this screen
// already accepted by the mailbox, and this screen cannot add one.

import { TRANSCRIPT_WHO } from './transcript.js';

/** The element type names the screen uses, in one place so a test can name
 * them too. They are the react-native primitives the entry passes in. */
export const SCREEN = Object.freeze({
  ROOT: 'fugaro.text',
  TITLE: 'fugaro.text.title',
  CONVERSATION: 'fugaro.text.conversation',
  CONVERSATION_LABEL: 'fugaro.text.conversation.label',
  LINE: 'fugaro.text.conversation.line',
  LINE_WHO: 'fugaro.text.conversation.line.who',
  LINE_TEXT: 'fugaro.text.conversation.line.text',
  INSTALLATION: 'fugaro.text.installation',
  INSTALLATION_NAME: 'fugaro.text.installation.name',
  PROMPT: 'fugaro.text.prompt',
  DESTRUCTIVE: 'fugaro.text.prompt.destructive',
  NOTHING: 'fugaro.text.nothing',
  RECENT: 'fugaro.text.recent',
  SUMMARY: 'fugaro.text.summary',
  UNSENT: 'fugaro.text.unsent',
  CONFIRMATION: 'fugaro.text.confirmation',
  READ_BACK: 'fugaro.text.confirmation.read-back',
  NOTICE: 'fugaro.text.notice',
  INPUT: 'fugaro.text.input',
});

/** The screen's title. A constant, never a value from an account. */
export const TEXT_TITLE = 'Text';

/** What a section says when its installation has nothing waiting. */
export const NO_PROMPTS = 'Nothing waiting.';

/** What a section says when the app has no recent summaries for it. */
export const NO_SUMMARIES = 'No recent activity.';

/** The word drawn beside a destructive question. */
export const DESTRUCTIVE_WORD = 'needs confirmation';

/** The label above a section's recent summaries. */
export const RECENT_LABEL = 'Recent';

/** The label above the one conversation voice and text share (D2788). */
export const CONVERSATION_LABEL = 'Conversation';

/** What the conversation says when nothing has been said in this session yet. */
export const NO_CONVERSATION = 'Nothing said yet.';

/**
 * Who said it, one word each. Both views draw the same lines, so a line has to
 * say whose it is or the conversation reads as one voice: the owner's own words
 * and the agent's reply are the two halves D2788's "one transcript" joins. The
 * words are keyed by the transcript's own `who` values rather than written
 * twice, so a third one cannot be drawn by accident -- `conversationLines`
 * refuses a line whose `who` is not one of these.
 */
export const SPEAKER_WORD = Object.freeze({
  [TRANSCRIPT_WHO.OPERATOR]: 'You',
  [TRANSCRIPT_WHO.AGENT]: 'Agent',
});

/**
 * How many lines of the conversation one screen shows: the most recent ones, in
 * the order they were said. The transcript itself keeps every line -- a lost
 * line is the one thing it may not do -- so this is the screen's bound and not
 * the store's, named here so "what is on screen" is a fact a test states.
 */
export const CONVERSATION_LIMIT = 50;

/** What the input is for, in one line, so an empty screen is not a puzzle. */
export const INPUT_HINT = 'Type a message, or "Fugaro, …" for a command.';

/**
 * How many recent summaries one section shows. A phone screen holds a handful,
 * and the summaries are the app's own store rather than a paged feed: the cap
 * is here, named, so the screen's size does not depend on how long the app has
 * been running.
 */
export const RECENT_SUMMARY_LIMIT = 5;

/**
 * The most recent `limit` summaries, newest first.
 *
 * `summaries` is the app's list (`{ id, at, text }`) in whatever order it
 * holds; this function is the ordering, and it is a pure one so the test can
 * assert the rule rather than the store. An entry that is not shaped like a
 * summary is dropped rather than rendered: a row with no text is a blank line
 * a reader cannot tell from a bug, and this is the one place the screen decides
 * what it will draw.
 *
 * Ties are broken by id, so two summaries with the same timestamp have one
 * order rather than the store's.
 */
export function recentSummaries(summaries, limit = RECENT_SUMMARY_LIMIT) {
  if (!Array.isArray(summaries)) return Object.freeze([]);
  if (!Number.isSafeInteger(limit) || limit < 0) return Object.freeze([]);
  const rows = summaries.filter(
    (one) =>
      one !== null &&
      typeof one === 'object' &&
      typeof one.text === 'string' &&
      one.text !== '' &&
      Number.isSafeInteger(one.at),
  );
  const ordered = [...rows].sort((a, b) => {
    if (a.at !== b.at) return b.at - a.at;
    const left = String(a.id);
    const right = String(b.id);
    if (left === right) return 0;
    return left < right ? 1 : -1;
  });
  return Object.freeze(ordered.slice(0, limit));
}

/**
 * The conversation lines this screen will draw: the most recent `limit`, oldest
 * first, and nothing else.
 *
 * The same rule `recentSummaries` applies, for the same reason and with one
 * difference: a line is drawn only when it carries a non-empty string and a
 * `who` this screen has a word for, so a malformed value cannot reach the tree
 * as a blank row or an unnamed speaker -- and an empty production here is an
 * **empty list**, never a guess, which is what makes "the view shows the one
 * transcript and invents nothing" checkable.
 *
 * `limit` is a tail and not a head: the newest lines are the ones the owner is
 * looking for after a switch, and a conversation that grew past the screen's
 * bound must not show the start of the session and hide the reply. A `limit` of
 * zero is nothing at all, which is a value the caller may ask for.
 */
export function conversationLines(entries, limit = CONVERSATION_LIMIT) {
  if (!Array.isArray(entries)) return Object.freeze([]);
  if (!Number.isSafeInteger(limit) || limit < 0) return Object.freeze([]);
  if (limit === 0) return Object.freeze([]);
  const rows = entries.filter(
    (one) =>
      one !== null &&
      typeof one === 'object' &&
      typeof one.text === 'string' &&
      one.text !== '' &&
      Object.hasOwn(SPEAKER_WORD, one.who),
  );
  return Object.freeze(rows.slice(-limit));
}

/**
 * The entries the section will draw, and nothing else.
 *
 * A P6.5 listing entry is `{ prompt, destructive, safe, announcedAt, stage,
 * due }` with the prompt itself P6.3's message, so one is drawn only when it
 * has a `prompt` object carrying a non-empty string id. That is the same rule
 * `recentSummaries` applies to summaries and it is here for the same reason: a
 * malformed entry is a value this screen refuses to draw, and one that reached
 * the tree unguarded would be a **render crash** (`null.prompt`) rather than a
 * blank row -- a crash on the screen whose whole job is to be the fallback when
 * the voice path is not working.
 *
 * The same `filter` in `promptLine`'s place means an entry the caller passed
 * with a text but no usable prompt cannot invent one: what is drawn is the
 * prompt's own sentence, and it is the entry's own id that is drawn only when
 * the sentence is missing, never a value the screen composed.
 */
function drawablePrompts(list) {
  if (!Array.isArray(list)) return Object.freeze([]);
  return list.filter((one) => {
    if (one === null || typeof one !== 'object') return false;
    const prompt = one.prompt;
    if (prompt === null || typeof prompt !== 'object') return false;
    return typeof prompt.id === 'string' && prompt.id !== '';
  });
}

/** One drawable prompt, as the screen says it: its own sentence, or its id. */
function promptLine(entry) {
  const prompt = entry.prompt;
  return typeof prompt.text === 'string' && prompt.text !== '' ? prompt.text : prompt.id;
}

/**
 * Build the text screen.
 *
 * Every argument is required and checked here rather than trusted, for the same
 * reason `createRoot` checks its own: a screen built without the primitives
 * fails at the first render with an error a long way from the cause.
 *
 * The component takes `{ installations, conversation, confirmation, unsent,
 * notice }`:
 *
 *   * `installations` -- `[{ account, pending, summaries }]`, one section each,
 *     in the caller's order (P6.5's `pendingAcross` accounts and the app's
 *     summaries). Nothing is summed across them;
 *   * `conversation` -- the one transcript's lines (`transcript.entries()`),
 *     oldest first: `{ id, at, who, source, text, engine }` as `transcript.js`
 *     records them. The most recent `CONVERSATION_LIMIT` of them are drawn, and
 *     the source of a line (voice, text, the outbox) is **read by nothing
 *     here**: which door the words came through is D2788's record and not a
 *     difference the owner should see, because the whole point of one
 *     transcript is that a line is a line. The screen is handed a list rather
 *     than the transcript, so it stays a pure function of its props -- the
 *     caller appends and re-renders;
 *   * `confirmation` -- `null`, or the read-back to show (`{ readBack,
 *     deadline }`, as `input.js`'s `awaiting()` and `propose()` return it). It
 *     is drawn **verbatim and once**: the read-back that module hands out
 *     already ends in the typed cue (`typedReadBack` swaps P6.2's spoken one
 *     for it where the sentence is built), so the screen adds no second
 *     instruction under it. Drawing P6.2's own `readBack` here instead would
 *     put a "Say" line and a "Type" line on the same screen -- the
 *     contradiction this arrangement exists to prevent;
 *   * `unsent` -- the mailbox client's **`unsent()`**, not its `pending()`: the
 *     text the gate REFUSED, `[{ text, at, code }]`, oldest first. D167's first
 *     half is that this is SHOWN rather than handed to the SDK's queue, so a
 *     user who typed into a dead connection can see the words are still theirs
 *     and say them again. `pending()` is the other list and must not be passed
 *     here: it is what the client is ALREADY re-delivering itself (`landed`) or
 *     holds for a retry, so drawing it as unsent would invite the owner to
 *     retype a sentence the consumer is already acting on -- `client.js` says
 *     the same thing from its side ("a screen resending from here would be a
 *     second write of a record already in flight");
 *   * `notice` -- one line the app has to say (a refusal's own code, or that a
 *     confirmed action was not delivered). A string, or null.
 *
 * The component is pure: it renders the props it is given and returns no
 * element that could write anything. The keyboard is `TextInput` with a
 * `value` of the caller's draft and an `onChangeText` the caller passes, so the
 * draft lives in the app rather than in the tree.
 */
export function createTextScreen({ React, View, Text, TextInput }) {
  if (React === null || typeof React !== 'object' || typeof React.createElement !== 'function') {
    throw new TypeError('createTextScreen needs React (with createElement)');
  }
  if (typeof View !== 'function' && typeof View !== 'object') {
    throw new TypeError('createTextScreen needs react-native\'s View');
  }
  if (typeof Text !== 'function' && typeof Text !== 'object') {
    throw new TypeError('createTextScreen needs react-native\'s Text');
  }
  if (typeof TextInput !== 'function' && typeof TextInput !== 'object') {
    throw new TypeError('createTextScreen needs react-native\'s TextInput');
  }

  const el = React.createElement;

  /** One installation's section: its name, its questions, its recent lines. */
  function section(entry) {
    const account = entry.account;
    const pending = drawablePrompts(entry.pending);
    const summaries = recentSummaries(entry.summaries);
    const children = [el(Text, { key: `${account}:name`, type: SCREEN.INSTALLATION_NAME }, account)];

    if (pending.length === 0) {
      children.push(el(Text, { key: `${account}:nothing`, type: SCREEN.NOTHING }, NO_PROMPTS));
    } else {
      pending.forEach((one, index) => {
        children.push(
          el(Text, { key: `${account}:prompt:${index}`, type: SCREEN.PROMPT }, promptLine(one)),
        );
        if (one.destructive === true) {
          children.push(
            el(Text, { key: `${account}:destructive:${index}`, type: SCREEN.DESTRUCTIVE }, DESTRUCTIVE_WORD),
          );
        }
      });
    }

    children.push(el(Text, { key: `${account}:recent`, type: SCREEN.RECENT }, RECENT_LABEL));
    if (summaries.length === 0) {
      children.push(el(Text, { key: `${account}:no-summaries`, type: SCREEN.SUMMARY }, NO_SUMMARIES));
    } else {
      summaries.forEach((one) => {
        children.push(el(Text, { key: `${account}:summary:${one.id}`, type: SCREEN.SUMMARY }, one.text));
      });
    }

    return el(View, { key: account, type: SCREEN.INSTALLATION }, ...children);
  }

  return function TextScreen(props = {}) {
    const installations = Array.isArray(props.installations) ? props.installations : [];
    const conversation = conversationLines(props.conversation);
    const unsent = Array.isArray(props.unsent) ? props.unsent : [];
    const children = [el(Text, { key: 'title', type: SCREEN.TITLE }, TEXT_TITLE)];

    // The one conversation, first: it is what the owner came back to after
    // switching from the voice view, and it is the same list the voice view
    // draws (D2788). One line says who said it and then the words, in the
    // transcript's own order -- never re-sorted, never filtered by where the
    // line came from.
    const lines = [
      el(Text, { key: 'conversation:label', type: SCREEN.CONVERSATION_LABEL }, CONVERSATION_LABEL),
    ];
    if (conversation.length === 0) {
      lines.push(el(Text, { key: 'conversation:nothing', type: SCREEN.NOTHING }, NO_CONVERSATION));
    } else {
      conversation.forEach((one, index) => {
        lines.push(
          el(
            View,
            // The transcript's ids are its own, and a caller may hand this
            // screen a list it built; the position is the one key that is
            // always here and always unique within one render.
            { key: `conversation:${index}`, type: SCREEN.LINE, who: one.who },
            el(Text, { key: 'who', type: SCREEN.LINE_WHO }, SPEAKER_WORD[one.who]),
            el(Text, { key: 'text', type: SCREEN.LINE_TEXT }, one.text),
          ),
        );
      });
    }
    children.push(el(View, { key: 'conversation', type: SCREEN.CONVERSATION }, ...lines));

    installations.forEach((one) => {
      if (one === null || typeof one !== 'object') return;
      children.push(section(one));
    });

    // The unsent text is the user's own and has NOT reached the agent: it is
    // shown above the keyboard, in the order the client holds it.
    //
    // **What this list is, and what it is not** (see the prop doc above). It is
    // the client's `unsent()`, which is D167's "the text the owner can say
    // again": the gate REFUSED each of these, so the client never wrote it and
    // never will. It is not `pending()` -- that list is the client's own retry
    // queue, every entry of which is on its way to the consumer already. There
    // is no `landed` flag to filter on here, because nothing in this list has
    // landed; the filter the screen used to carry was written for the other
    // list's shape, and a caller passing `pending()` would have had `landed:
    // false` entries (the ones waiting on a retry) drawn as unsent text. What
    // keeps the two apart is the caller, and the prop doc names the one to pass.
    unsent.forEach((one, index) => {
      if (one === null || typeof one !== 'object') return;
      if (typeof one.text !== 'string' || one.text === '') return;
      children.push(
        // `unsent()`'s entries carry no id (there is no record; nothing was
        // written), so the key is the position the client holds them in.
        el(Text, { key: `unsent:${index}`, type: SCREEN.UNSENT }, one.text),
      );
    });

    // The read-back is one line and it carries its own instruction -- the typed
    // one, put there by `typedReadBack` where P6.2 built the sentence. The
    // screen adds no second cue under it: a "Say" line above a "Type" line is
    // the contradiction this screen cannot afford, and the screen is not where
    // the swap belongs (a caller that draws the same read-back elsewhere must
    // get the same instruction).
    const confirmation = props.confirmation;
    if (
      confirmation !== null &&
      confirmation !== undefined &&
      typeof confirmation.readBack === 'string' &&
      confirmation.readBack !== ''
    ) {
      children.push(
        el(
          View,
          { key: 'confirmation', type: SCREEN.CONFIRMATION },
          el(Text, { key: 'read-back', type: SCREEN.READ_BACK }, confirmation.readBack),
        ),
      );
    }

    if (typeof props.notice === 'string' && props.notice !== '') {
      children.push(el(Text, { key: 'notice', type: SCREEN.NOTICE }, props.notice));
    }

    children.push(
      el(TextInput, {
        key: 'input',
        type: SCREEN.INPUT,
        value: typeof props.draft === 'string' ? props.draft : '',
        onChangeText: typeof props.onChangeText === 'function' ? props.onChangeText : noop,
        placeholder: INPUT_HINT,
      }),
    );

    return el(View, { type: SCREEN.ROOT }, ...children);
  };
}

function noop() {}
