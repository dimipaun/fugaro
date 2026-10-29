// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The view's rules: the newest document, the day break, the degraded shape,
// and silence that is not death (mobile plan, Task P3.2; json-contract §10.13
// rules 2, 3 and 4, §10.7, §10.8).
//
// The reader (`reader.js`) hands documents in and the screen asks this module
// what to draw. Every rule below is one of the plan's named cases, and each is
// made structural rather than advisory -- a screen cannot get it wrong by
// forgetting it:
//
//   * **the newest document, never accumulated** (rule 2). The view holds one
//     document and each `ingest` **replaces** it. Nothing is merged, nothing is
//     carried over: a `repos` list of two entries followed by one of a single
//     entry leaves one entry, and a `counted` that fell leaves the lower
//     number. There is no arithmetic in this file, so there is nothing that
//     *could* accumulate.
//   * **a changed `day` is a break in the series, never a zero** (§10.7). Two
//     facts decide what may be drawn: the day changed under the view, and
//     whether the day's counters have arrived at all (`measured`). A document
//     whose day has not delivered a `put` yet is a **gap** -- and when the view
//     is broken its `drawable` is `null`, so the zero the document carries
//     (the CLI's own rollover document carries `counted_usd: 0`) is not on
//     screen and cannot be drawn by a screen that forgot to check.
//   * **the degraded shape renders as degraded** (§10.8, §2 (iii)). It has no
//     connection and no figures at all, and the view says so instead of
//     rendering the absence as zero spend.
//   * **silence is not death** (rule 3). Liveness is decided from the
//     document's `now`, its `connection.state`, its `connection.age_seconds`
//     and **the phone's own clock**, exactly the four inputs the rule names.
//     The vocabulary has **no word for a dead process**: a feed that has
//     stopped arriving goes `stale` after 45 seconds and stays `stale`, and the
//     last document's numbers stay on screen -- they were measured, and a
//     silence does not unmeasure them.
//
// Rules 1 and 5 are `document.js`'s (a line that is not an object is skipped;
// a missing field is `unavailable`). Rule 6 is `--repo`, which the phone does
// not have: its repository list is the owner's (`paths.js`), and `len(repos)`
// is that list, not a filtered project's. Rule 7 is about a child process, and
// the phone has none: there is nothing to expect to end and nothing to loop on.
// Rule 8 is the console's allow-list.

import { figures } from './document.js';

/** What the feed is doing, in the only words this app has for it. There is no
 * `dead` and no `exited`: a feed that stops arriving is a silence, and the
 * states below say how long it has been quiet. */
export const LIVENESS = Object.freeze(['no-document', 'live', 'stale', 'offline', 'refused', 'polling', 'degraded']);

/** `watch.StaleAfter` (§10.9): the silence after which a **stream** is `stale`.
 * It is the only silence threshold the connection has: `State.conn` reads the
 * oldest heard stream's age and never leaves `stale` for it. */
export const STALE_AFTER_MS = 45_000;

// **`watch.LostAfter` is deliberately not here.** §10.9 lists it beside
// `StaleAfter`, but as a property of a **run**, not of the connection: it is
// the silence after which `State.health` calls a run `lost` (and `SilentAfter`,
// 60 s, the one before that). The phone has no run -- it is not subscribed to
// `runs/**` or `agents/**`, and nothing it reads has a deadline or a stage --
// so a 180-second threshold applied to this connection would be a rule about
// runs dressed as a rule about streams, and it would say a feed was `offline`
// while the CLI, reading the same tree at the same instant, said `stale`. The
// app's `offline` is the reader's own: a port that errored, or a plan whose
// paths have not all answered yet.

// **`watchFirstFrame` is deliberately not here.** §10.9 lists it as the head
// start the CLI's four streams get ("the four streams' head start before the
// first document, readable or not") -- a constant about a **child process's**
// start-up, which the phone does not have. An earlier version exported it as a
// threshold nothing read: a number on the door that looked load-bearing and
// decided nothing. The phone's "nothing has arrived yet" is `no-document`, and
// it needs no clock.

/** What the screen says when a line or a node value was not a document. A
 * **static** sentence: the thing that was skipped was not parsed, so nothing
 * about it can be shown (§10.8's "surface it as a header warning", without
 * echoing a node's contents back at the user). */
export const SKIP_NOTICE = 'part of the feed could not be read';

/** What the screen says when a document claimed to be one and was refused. */
export const REFUSED_NOTICE = 'the feed sent something this app does not understand';

/** A refusal is not a skip: the difference is for diagnostics, not for the
 * screen, which draws neither. */
function noticeFor(judged) {
  return judged.skipped === true ? SKIP_NOTICE : REFUSED_NOTICE;
}

/**
 * The view of the feed.
 *
 * One instance per account: a switch builds a new one, so nothing survives an
 * account change (`accounts/store.js`).
 */
export class FeedView {
  #document = null;
  #arrivedAt = null;
  #day = null;
  #measured = false;
  #dayChanged = false;
  #note = null;
  #ingests = 0;

  /**
   * Take one decoded thing: a judged document (`{ok: true, document}`) or a
   * skip or refusal from `document.js`.
   *
   * `measured` is whether the document was built from a counter node that has
   * actually arrived for its own day -- the phone's equivalent of the CLI's
   * `put` of `/` after a rollover (§10.7). It is `reader.js`'s fact, not the
   * document's: the document shape is the contract's and carries no such field.
   *
   * Returns `{ ok, dayChanged }`, so a caller can see the break without asking
   * for a snapshot.
   */
  ingest(judged, { nowMs, measured = true } = {}) {
    if (judged === null || typeof judged !== 'object' || judged.ok !== true) {
      // A skip or a refusal: the last document stays. A line this app could not
      // read is not a reason to blank a screen that is showing a measurement.
      if (judged !== null && typeof judged === 'object' && typeof judged.code === 'string') {
        this.#note = noticeFor(judged);
      }
      return Object.freeze({ ok: false, dayChanged: false });
    }
    const document = judged.document;
    const changed = this.#day !== null && document.degraded !== true && document.day !== this.#day;
    if (document.degraded !== true) {
      this.#day = document.day;
      this.#measured = measured === true;
      // The break is **sticky until the new day has measured something**.
      // §10.7's gap lasts from the rollover until the day's counter arrives,
      // and the reader assembles more than once inside that gap -- the rollover
      // itself, then whatever else the tree delivers (a cap, a kill switch)
      // before the counter does. A flag that the first of those cleared would
      // report a day break that nothing could ever observe, which is the same
      // as not reporting it at all.
      this.#dayChanged = changed || (this.#dayChanged && !this.#measured);
    } else {
      this.#dayChanged = false;
    }
    this.#document = document;
    this.#arrivedAt = nowMs;
    this.#note = null;
    this.#ingests += 1;
    return Object.freeze({ ok: true, dayChanged: this.#dayChanged });
  }

  /**
   * What to draw at `nowMs`, on the phone's clock.
   *
   * `broken` is the day-break rule: the day's numbers are not a measurement
   * yet, so the view has nothing drawable and the screen draws the gap. That is
   * the whole of §10.7's "must not draw the zero", expressed as an absence.
   *
   * `liveness` never says the feed died. `silenceMs` is how long it has been
   * quiet, from the phone's own clock and the document's own age; it keeps
   * growing while the numbers stay on screen.
   */
  snapshot({ nowMs } = {}) {
    const document = this.#document;
    if (document === null) {
      return Object.freeze({
        liveness: 'no-document',
        document: null,
        day: null,
        dayChanged: false,
        broken: true,
        drawable: null,
        degraded: false,
        silenceMs: null,
        ageMs: null,
        note: this.#note,
        ingests: this.#ingests,
      });
    }
    const degraded = document.degraded === true;
    const ageMs = elapsed(nowMs, this.#arrivedAt);
    // The rule's four inputs: the phone's own clock (`ageMs`), the document's
    // `connection.age_seconds`, its `connection.state`, and its `now` -- which
    // is the age of the *reading*, and is what makes a document that arrives
    // promptly but describes an old instant still count as quiet.
    const describedAgeMs = documentAge(document, nowMs);
    const silenceMs = Math.max(ageMs, describedAgeMs, degraded ? 0 : connectionAge(document));
    const broken = !degraded && !this.#measured;
    return Object.freeze({
      liveness: livenessOf(document, silenceMs, degraded),
      document,
      day: degraded ? null : document.day,
      dayChanged: this.#dayChanged,
      broken,
      // The one thing a screen draws: nothing at all when the series is broken
      // or the feed is degraded. `figures()` is here so that a caller which
      // wants to judge every figure of what it is drawing has them in one list.
      drawable: broken || degraded ? null : Object.freeze({ total: document.total, repos: document.repos, figures: figures(document) }),
      degraded,
      silenceMs,
      ageMs,
      note: this.#note,
      ingests: this.#ingests,
    });
  }
}

/** The phone's clock since an instant, floored at zero: a document whose
 * instant is ahead of this phone's clock is not a negative age, and it is not
 * an age at all -- check 84 (P4.2) owns "clock differs". */
function elapsed(nowMs, thenMs) {
  if (typeof nowMs !== 'number' || !Number.isFinite(nowMs)) return null;
  if (typeof thenMs !== 'number' || !Number.isFinite(thenMs)) return null;
  return Math.max(nowMs - thenMs, 0);
}

/** How old the reading is, from the document's own `now`. */
function documentAge(document, nowMs) {
  const at = Date.parse(document.now);
  if (!Number.isFinite(at)) return 0;
  return elapsed(nowMs, at);
}

/** The CLI's own measure of how quiet its streams are. */
function connectionAge(document) {
  if (document.connection === undefined) return 0;
  return Math.max(document.connection.ageSeconds * 1000, 0);
}

/**
 * The state, from the worst of what is known.
 *
 * `refused` outranks silence: a refusal is a diagnosis ("this token may not
 * read this node") and the silence that follows it is its consequence, not a
 * separate fact. Everything else is the worse of the document's own state and
 * the phone's own silence, in `State.conn`'s order -- and **silence only ever
 * reaches `stale`**, because that is as far as `State.conn` goes: a stream that
 * has gone quiet is `stale` at 45 seconds and stays `stale` at an hour. A feed
 * whose numbers are still on screen and whose clock has run away has not
 * become `offline`; `offline` is the reader's own news (a port error, a plan
 * that has not answered), and it arrives on the document.
 */
function livenessOf(document, silenceMs, degraded) {
  if (degraded) return silenceMs > STALE_AFTER_MS ? 'stale' : 'degraded';
  const state = document.connection.state;
  if (state === 'refused') return 'refused';
  if (state === 'offline') return 'offline';
  if (silenceMs > STALE_AFTER_MS) return 'stale';
  if (state === 'stale') return 'stale';
  if (state === 'polling') return 'polling';
  return 'live';
}
