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
// **What is not here.** No answer buttons: a typed answer goes through the
// caller's queue (`shared/prompts`' `answer`) and this screen is handed the
// keyboard's text, not the prompt it answers -- matching a typed line to a
// waiting question is the caller's, because the matching rule ("the active
// installation's oldest question", §3.4) is state this screen does not hold.
// No launch button either: a launch is D162's pass-through and belongs to the
// action it starts.

/** The element type names the screen uses, in one place so a test can name
 * them too. They are the react-native primitives the entry passes in. */
export const SCREEN = Object.freeze({
  ROOT: 'fugaro.text',
  TITLE: 'fugaro.text.title',
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
 * The component takes `{ installations, confirmation, unsent, notice }`:
 *
 *   * `installations` -- `[{ account, pending, summaries }]`, one section each,
 *     in the caller's order (P6.5's `pendingAcross` accounts and the app's
 *     summaries). Nothing is summed across them;
 *   * `confirmation` -- `null`, or the read-back to show (`{ readBack,
 *     deadline }`, as `input.js`'s `awaiting()` and `propose()` return it). It
 *     is drawn **verbatim and once**: the read-back that module hands out
 *     already ends in the typed cue (`typedReadBack` swaps P6.2's spoken one
 *     for it where the sentence is built), so the screen adds no second
 *     instruction under it. Drawing P6.2's own `readBack` here instead would
 *     put a "Say" line and a "Type" line on the same screen -- the
 *     contradiction this arrangement exists to prevent;
 *   * `unsent` -- the mailbox client's `pending()`: the text that has not
 *     reached the agent yet (D167's first half is that this is SHOWN rather
 *     than queued by the SDK, so a user who typed into a dead connection can
 *     see it is still theirs);
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
    const unsent = Array.isArray(props.unsent) ? props.unsent : [];
    const children = [el(Text, { key: 'title', type: SCREEN.TITLE }, TEXT_TITLE)];

    installations.forEach((one) => {
      if (one === null || typeof one !== 'object') return;
      children.push(section(one));
    });

    // The unsent text is the user's own and has NOT reached the agent: it is
    // shown above the keyboard, in the order the client holds it.
    //
    // `landed: true` is **not** drawn here, and that is the point of the flag.
    // D167(a) and P3.6's own `pending()` reserve "the unsent text the owner is
    // shown" for a message the database has NOT received; a landed one has been
    // received, and a screen that drew it the same way would invite the owner
    // to retype a sentence the consumer is already acting on -- a duplicate
    // command from a screen whose failure mode is meant to be the safe one.
    unsent.forEach((one, index) => {
      if (one === null || typeof one !== 'object') return;
      if (one.landed === true) return;
      if (typeof one.text !== 'string' || one.text === '') return;
      children.push(
        el(Text, { key: `unsent:${typeof one.id === 'string' ? one.id : index}`, type: SCREEN.UNSENT }, one.text),
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
