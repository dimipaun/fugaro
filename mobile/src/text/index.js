// The text screen's one door (mobile plan, Task P6.9; D167).
//
// A screen imports `src/text/index.js`. It builds the typed pipeline with
// `createTypedInput` over the mailbox client it already has (P3.6's
// `createMailboxClient`, from `src/mailbox/`) and draws `createTextScreen` with
// react-native's primitives, exactly as `src/app/root.js` is drawn. Everything
// else here is the vocabulary those two answer in.
//
// **What is deliberately NOT exported here: anything that writes.** There is no
// store, no paths and no client in this module's exports: the mail is the
// caller's client, and this screen is handed it rather than building one.
// `src/mailbox/`'s `write-path.test.js` holds that fence, and the fastest way
// to break it would be a screen with a second way to reach a database.
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

export {
  DESTRUCTIVE_WORD,
  INPUT_HINT,
  NO_PROMPTS,
  NO_SUMMARIES,
  RECENT_LABEL,
  RECENT_SUMMARY_LIMIT,
  SCREEN,
  TEXT_TITLE,
  createTextScreen,
  recentSummaries,
} from './screen.js';
