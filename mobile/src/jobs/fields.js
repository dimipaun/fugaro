// The job list's field table, and the vocabulary the list is written in
// (mobile plan, Task P3.3; D112's answer, tension (b1), resolved 2026-10-17).
//
// **What the list is allowed to show, and why it is a table.** The resolution
// of 2026-10-17 narrowed this task: the phone reads only `config/caps/**`,
// `spend/$day/**`, `config/kill/**` and its own mailbox, so "the job list shows
// numbers and states only -- spend, caps, the kill state and the heartbeat --
// with no run titles, no repositories, no queued rows, no finished rows and no
// PR link". The plan's own added test case is "every figure is rendered from a
// granted family only", and a row written by hand can honour that today and
// break it with one line tomorrow. So each figure the list draws is an entry
// in `JOB_FIELDS`: its name, the family or families its number was read from,
// and the label the screen prints. `JOB_FAMILIES` is derived from the table,
// and `scope.test.js` judges every family in it against `src/feed/index.js`'s
// `FAMILIES` -- the four families the read grant names. A field read from
// somewhere else has to be added to this table first, and that test fails when
// it is.
//
// **What is deliberately NOT a field here.**
//
//   * **A run.** `agents/$slug/$run` carries no read for a user claim and
//     `outcomes/**` carries none either, so no field of this table can come
//     from a run: there is no run title, no stage, no deadline and no agent on
//     this screen. `scope.test.js` greps the module for both family names.
//   * **A repository.** `config/caps/repos/$slug` and `spend/$day/repos/$slug`
//     *are* granted, and P3.2's document carries a `repos[]` block built from
//     them -- but the resolution names repositories among the things the list
//     does not show, so this module renders **no repository row and no
//     repository name**. The project's own row is what is left, and
//     `scope.test.js` refuses the token `repos` anywhere in the module's
//     production files, so a later task that wants those rows has to say so in
//     this file rather than reach for the document quietly.
//   * **A queued or finished row, and a link.** Those rows live in the runs
//     bucket (`internal/cli/watch_queued.go`'s queue scanner, F14), which the
//     grant does not cover, and the link they would carry is F27's open
//     question. P0.3's validator (`shared/url/index.js`) is merged and stays the
//     gate for any link the app ever opens; this task builds none, so nothing
//     here imports it.
//   * **The mode.** `/config/mode` is not granted, so P3.2's document carries
//     `mode: null` rather than a guessed `observe`. Nothing here reads it.
//
// **The two states that are not figures.** A figure carries a number and an
// availability state (`src/feed/availability.js`) and refuses to exist without
// one; a kill switch and a heartbeat are not amounts, so they have their own
// vocabularies below, and both are closed sets: a value outside them is a
// refusal, never a rendered string.

/**
 * The list's refusal codes. Every way this module declines to build a list,
 * as a value a caller can branch on -- this app's convention
 * (`src/accounts/account.js`, `src/feed/reader.js`).
 */
export const LIST_CODES = Object.freeze({
  /** The feed snapshot is not one (`FeedReader.snapshot()`'s shape). */
  BAD_SNAPSHOT: 'bad-snapshot',
  /** The snapshot's document is not a document this app can render. */
  BAD_DOCUMENT: 'bad-document',
  /** A field of the row is not a figure, or the kill switch is not a state. */
  BAD_ROW: 'bad-row',
  /** The liveness is not one of the feed's seven words. */
  BAD_LIVENESS: 'bad-liveness',
  /** The presence reading is not one P1.4/P3.6 can produce. */
  BAD_PRESENCE: 'bad-presence',
  /** The clock is not an instant this app can render. */
  BAD_NOW: 'bad-now',
});

/**
 * The fields of the project's row, in the order the screen prints them.
 *
 * `families` names the granted families the number was **read from**, not the
 * family the field's own node lives in: `percent` and `burn` are recomputed on
 * the phone from both `spend/$day/**` and `config/caps/**` (P3.2's
 * `recompute.js`), which is why they carry two. The list is frozen and each
 * entry is frozen: a caller cannot widen the screen by pushing to it.
 */
export const JOB_FIELDS = Object.freeze([
  Object.freeze({ name: 'counted', label: 'Spend (counted)', families: Object.freeze(['spend']) }),
  Object.freeze({ name: 'spent', label: 'Spend (spent)', families: Object.freeze(['spend']) }),
  Object.freeze({ name: 'notional', label: 'Spend (notional)', families: Object.freeze(['spend']) }),
  Object.freeze({ name: 'dailyCap', label: 'Daily cap', families: Object.freeze(['config/caps']) }),
  Object.freeze({ name: 'percent', label: 'Cap used', families: Object.freeze(['spend', 'config/caps']) }),
  Object.freeze({ name: 'burn', label: 'Burn rate', families: Object.freeze(['spend', 'config/caps']) }),
  Object.freeze({ name: 'kill', label: 'Kill switch', families: Object.freeze(['config/kill']) }),
]);

/** Every granted family the table reads, sorted and deduplicated. The test
 * that pins "a granted family only" walks this against the feed's own
 * `FAMILIES`, so a family the read grant does not name cannot be added to a
 * field without failing that test first. */
export const JOB_FAMILIES = Object.freeze(
  [...new Set(JOB_FIELDS.flatMap((field) => field.families))].sort(),
);

/** The row's field names, in the table's order. */
export const ROW_FIELDS = Object.freeze(JOB_FIELDS.map((field) => field.name));

/**
 * The kill switch's two states, as the screen spells them. A switch is on or
 * off and there is nothing else it can be: `jsonKill` returns a nil pointer
 * when the switch is off (so **presence is the on-ness**, contract §2), and an
 * unreadable node is **on** (`State.kill`: a switch that cannot be read is a
 * switch that might be on, and the safe direction for a stop switch is to
 * believe it).
 */
export const KILL_STATES = Object.freeze(['on', 'off']);

/** The heartbeat's words. The first four are `shared/presence`'s (`STATE` --
 * P3.6's `poll` reports one of them, and an unverified or unreadable heartbeat
 * is `offline` there), and `unknown` is this module's: **no poll has reported
 * yet**, which is not `offline` and must never be `live`. The set is closed:
 * a presence reading carrying anything else is refused. */
export const HEARTBEAT_STATES = Object.freeze(['live', 'stale', 'offline', 'clock-differs', 'unknown']);

/** What a line says when the **thing itself** is absent -- the project, the day
 * -- as opposed to a field the document did not carry. A caption rather than a
 * figure, so it is not `shown`'s job, and deliberately **not** one of the other
 * two marks: `n/a` is `shown`'s word for an `unavailable` figure (`n/a` in the
 * screen's own words comes from there and from nowhere else, which is why this
 * module defines no second copy of it), and `GAP_MARK` (`—`) is the contract's
 * mark for a **broken series**. Three absences, three different strings; a test
 * in `lines.test.js` holds them apart. */
export const NOTHING = 'not known';
