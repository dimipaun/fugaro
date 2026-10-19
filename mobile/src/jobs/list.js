// The list itself: one row of numbers and states, the heartbeat, and the wire
// between them (mobile plan, Task P3.3; resolved 2026-10-17, tensions (a) and
// (b1)).
//
// Two things live here, and they are deliberately apart:
//
//   * **`jobList`** is a pure function of the feed's snapshot and the last
//     presence reading. It decides what the screen draws, and every rule it
//     applies is one of the plan's own cases: the day break is a **break** (a
//     broken series draws `row: null`, never a document's rollover zero), the
//     degraded document renders as degraded and not as a zero spend, the
//     heartbeat is `unknown` until something has read it, and nothing is
//     invented for a stream that has said nothing.
//   * **`createJobList`** is the wiring the feed's door names as this task's
//     ("nothing anywhere constructs a `FeedReader` or a database client yet,
//     and that wiring is P3.3's -- the job list is the first screen that needs
//     a feed"). It builds P3.2's reader from a **port it is handed** and P3.6's
//     presence report from a **function it is handed**: this module imports no
//     database SDK and no `firebase` module, so a second subscription to the
//     same project is not something an import can buy (D114's no-cross-leakage
//     rule, and P3.1's boundary scan). What it holds is one reader, one
//     presence reading and one clock.
//
// **Why the list draws no repository row.** `config/caps/repos/$slug` and
// `spend/$day/repos/$slug` are granted and the feed's document carries a block
// built from them, but the resolution of 2026-10-17 lists repositories among
// the things the job list does not show, so this module never reads that block
// (`fields.js` says so, and `scope.test.js` refuses the token in the module's
// production files). The project's row is what is left.
//
// **Nothing here is a claim about a run.** No field of the row comes from a run
// and no line names one; the feed's document cannot even carry a run, because
// `document.js` accepts the runs-bucket fields only as empty arrays.

import { FeedReader, LIVENESS } from '../feed/index.js';
import { LIST_CODES } from './fields.js';
import { heartbeatRow } from './heartbeat.js';
import { projectRow } from './row.js';

/** A frozen refusal, in the shape the app's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * The feed's snapshot, judged as this module's input.
 *
 * The snapshot is `FeedReader.snapshot()`'s -- `FeedView`'s fields plus the
 * reader's day and its paths -- and this judgement is about the parts the list
 * reads: the liveness must be one of the feed's seven words (a word outside
 * them is a state nobody defined, and printing it would be printing something
 * this app has never said), the document is a document or `null` (the feed has
 * sent nothing), and the day, the note and the two flags are the shapes
 * `view.js` produces.
 */
function checkSnapshot(snapshot) {
  if (snapshot === null || typeof snapshot !== 'object' || Array.isArray(snapshot)) {
    return refuse(LIST_CODES.BAD_SNAPSHOT, 'snapshot');
  }
  const liveness = snapshot.liveness;
  if (typeof liveness !== 'string' || !LIVENESS.includes(liveness)) {
    return refuse(LIST_CODES.BAD_LIVENESS, 'liveness');
  }
  const document = snapshot.document;
  if (document !== null && typeof document !== 'object') {
    return refuse(LIST_CODES.BAD_DOCUMENT, 'document');
  }
  const day = snapshot.day;
  if (day !== null && day !== undefined && typeof day !== 'string') {
    return refuse(LIST_CODES.BAD_SNAPSHOT, 'day');
  }
  const note = snapshot.note;
  if (note !== null && note !== undefined && typeof note !== 'string') {
    return refuse(LIST_CODES.BAD_SNAPSHOT, 'note');
  }
  return Object.freeze({
    ok: true,
    document: document ?? null,
    liveness,
    day: day ?? null,
    dayChanged: snapshot.dayChanged === true,
    broken: snapshot.broken === true,
    degraded: snapshot.degraded === true,
    note: note ?? null,
  });
}

/**
 * What the screen draws, from the feed's snapshot and the last presence
 * reading.
 *
 * Returns `{ ok: true, ... }` or a refusal. The list's own shape:
 *
 *   * `row` -- the project's row (`row.js`), or **`null`** when the series is
 *     broken, the feed is degraded, or nothing has arrived. `null` is the rule
 *     §10.7 states ("must not draw the zero"): a sighting of a new day before
 *     its counter has been heard is a **gap**, not a spend of zero, and a
 *     screen that drew the document anyway would print the rollover's zero as a
 *     measurement. `lines.js` draws the gap mark for it.
 *   * `heartbeat` -- P3.6's reading, always present, in five words
 *     (`fields.js`). It is a different family and a different stream from the
 *     spend the row carries, so a broken series does not blank it.
 *   * `connection` -- the feed's own liveness word, carried unchanged. This
 *     module does not interpret it: silence reaching `stale` and stopping there
 *     is `view.js`'s rule, and there is no word here for a dead process.
 *   * `day`, `dayChanged`, `broken`, `degraded`, `note` -- the feed's, carried
 *     for a screen that draws them.
 */
export function jobList({ snapshot, presence } = {}) {
  const judged = checkSnapshot(snapshot);
  if (!judged.ok) return judged;
  // `ok === false` again: `heartbeatRow` answers with a refusal or with the
  // state, and `unknown` -- the state of a heartbeat nobody has read -- has no
  // `ok` key.
  const heartbeat = heartbeatRow(presence);
  if (heartbeat.ok === false) return heartbeat;
  const document = judged.document;
  // A degraded feed has no figures at all, and a feed that has sent nothing
  // has said nothing about the day. Both draw no row, and neither is a zero.
  const broken = judged.broken || judged.degraded || document === null;
  let row = null;
  if (!broken) {
    const built = projectRow(document);
    if (!built.ok) return built;
    row = built.row;
  }
  return Object.freeze({
    ok: true,
    project: judged.degraded || document === null ? null : document.project,
    day: judged.day,
    dayChanged: judged.dayChanged,
    connection: judged.liveness,
    broken,
    degraded: judged.degraded,
    note: judged.note,
    row,
    heartbeat,
  });
}

/**
 * The list, wired to one account.
 *
 * **What it is handed, and what it hands back.** `port`, `account`,
 * `repositories`, `registry` and the clock go to P3.2's `FeedReader`, exactly
 * as that module's constructor takes them -- the account and the repository
 * list are the owner's (D114), and the registry is what keeps a listener from
 * outliving the account it was opened for. `readPresence` is a function that
 * returns **P3.6's `poll()` report** (`{ ok: true, presence }`) when the app has
 * a mailbox client, and is omitted when it has none: the heartbeat is then
 * `unknown` -- nothing has read it -- which is not `offline`.
 *
 * **The readers are not this module's to build twice.** There is one
 * `FeedReader` and there is one presence report; `snapshot()` is a read of
 * what they last said and starts nothing.
 */
export class JobList {
  #reader;
  #readPresence;
  #now;
  #presence = null;

  constructor({ reader, readPresence = null, now = () => Date.now() } = {}) {
    if (reader === null || typeof reader !== 'object' || typeof reader.snapshot !== 'function') {
      throw new TypeError('JobList needs a feed reader');
    }
    if (readPresence !== null && typeof readPresence !== 'function') {
      throw new TypeError('readPresence must be a function or null');
    }
    if (typeof now !== 'function') throw new TypeError('now must be a function');
    this.#reader = reader;
    this.#readPresence = readPresence;
    this.#now = now;
  }

  /** Subscribe the feed. The clock is the caller's instant or this list's own
   * clock -- one clock, the same one the reader was built with. */
  start(nowMs) {
    return this.#reader.start(nowMs ?? this.#now());
  }

  /** Move to the day the clock is in, if it has moved (§10.7's break). */
  refresh(nowMs) {
    return this.#reader.refresh(nowMs ?? this.#now());
  }

  /** Stop the feed's listeners. The last presence reading is kept: a stopped
   * feed does not unmake what the mailbox last said. */
  stop() {
    return this.#reader.stop();
  }

  /**
   * Read the other end's heartbeat, through the report function the app wired.
   *
   * With no function there is nothing to read and the answer says so
   * (`polled: false`), rather than manufacturing an `offline`. A refusal from
   * the mailbox client is passed through unchanged: its codes are its own
   * (`CLIENT_CODES`, P1.4's and the schema's), and this module does not
   * re-label another module's decision.
   */
  async poll() {
    if (typeof this.#readPresence !== 'function') {
      return Object.freeze({ ok: true, polled: false });
    }
    const report = await this.#readPresence();
    if (report === null || typeof report !== 'object' || typeof report.ok !== 'boolean') {
      return refuse(LIST_CODES.BAD_PRESENCE, 'presence');
    }
    if (!report.ok) return report;
    const heartbeat = heartbeatRow(report.presence);
    if (heartbeat.ok === false) return heartbeat;
    // The raw reading is what is kept, and `jobList` judges it once per
    // snapshot: storing the judged row instead would be a second judge of the
    // same value, with the two able to disagree.
    this.#presence = report.presence ?? null;
    return Object.freeze({ ok: true, polled: true, heartbeat });
  }

  /** What to draw, from what the feed and the mailbox last said. */
  snapshot({ nowMs = this.#now() } = {}) {
    return jobList({ snapshot: this.#reader.snapshot({ nowMs }), presence: this.#presence });
  }
}

/**
 * Build the list the way the app builds it: P3.2's reader over an injected
 * port, plus the presence report, plus the phone's clock.
 *
 * The reader's own refusals (a bad port, a closed account, a repository list
 * the feed refuses) arrive from `start()` and `refresh()`, unchanged.
 */
export function createJobList({
  port,
  account,
  repositories,
  registry,
  readPresence = null,
  now = () => Date.now(),
  view,
  sampler,
} = {}) {
  const reader = new FeedReader({ port, account, repositories, registry, now, view, sampler });
  return new JobList({ reader, readPresence, now });
}
