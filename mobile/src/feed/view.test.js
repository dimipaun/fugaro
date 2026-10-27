// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The view's rules (mobile plan, Task P3.2; json-contract §10.13 rules 2, 3 and
// 4, §10.7, §10.8).
//
// The plan names four of these cases, and this file is where three of them
// live:
//
//   * **the newest document is not accumulated onto the previous one** --
//     asserted as arithmetic that did *not* happen (a spend that fell stays
//     fallen) and as a list that got shorter.
//   * **a changed `day` is treated as a break** -- and the day's zero is not
//     drawn, which is §10.7's rule and the reason `drawable` is `null`.
//   * **the degraded shape renders as degraded rather than as zero spend** --
//     and it has no figures at all, so nothing can render a zero.
//   * **a feed that stops arriving is not treated as a dead process** -- the
//     states are silences with a length, the last numbers stay on screen, and
//     the vocabulary has no word for a process that died.

import assert from 'node:assert/strict';
import test from 'node:test';

import { STATE, isFigure, shown } from './availability.js';
import { decode, validateDocument } from './document.js';
import { LIVENESS, SKIP_NOTICE, STALE_AFTER_MS, FeedView } from './view.js';
import * as viewModule from './view.js';

const T0 = Date.parse('2025-11-09T12:00:00Z');

/** Long enough to be past every threshold this app has, and then some. */
const VERY_QUIET_MS = 30 * 60_000;

/** A wire total, so a case can change one number of it. */
function total(overrides = {}) {
  return {
    counted_usd: 12.5,
    spent_usd: 12.5,
    notional_usd: 40,
    daily_cap_usd: 50,
    percent: 25,
    burn: { known: true, usd_per_min: 0.05, fast: false },
    kill: null,
    runs: 0,
    run_hours: 0,
    ...overrides,
  };
}

/** A document as `nodes.js` and `document.js` would produce one. */
function document(overrides = {}) {
  const wire = {
    project: 'fugaro',
    mode: 'observe',
    day: '2025-11-09',
    now: new Date(T0).toISOString(),
    connection: { state: 'live', age_seconds: 0 },
    total: total(overrides.total),
    repos: [],
    ...overrides,
  };
  const judged = validateDocument(wire);
  assert.equal(judged.ok, true, JSON.stringify(judged));
  return judged;
}

/** A view that has already taken one document, which is the state most of
 * these cases are about. */
function shown_(judged, { nowMs = T0, measured = true } = {}) {
  const view = new FeedView();
  view.ingest(judged, { nowMs, measured });
  return view;
}

test('the thresholds are the CLI\'s own, and the run\'s is not one of them', () => {
  // §10.9's table. A test that only used the imported constants could not catch
  // a constant that had drifted from the console's.
  assert.equal(STALE_AFTER_MS, 45_000);
  // `watch.LostAfter` (180 s) is §10.9's too, and it is **not** this module's:
  // the table calls it a property of a *run* (`State.health`), and the phone is
  // subscribed to no run. A connection that used it would say a quiet feed was
  // `offline` while `State.conn`, reading the same tree, still said `stale`.
  assert.equal('LOST_AFTER_MS' in viewModule, false);
  assert.equal('SILENT_AFTER_MS' in viewModule, false);
  // `watchFirstFrame` (5 s) is §10.9's as well, and it is about a **child
  // process's** start-up -- the head start the CLI's four streams get before its
  // first document. The phone has no child and no four streams to start, and
  // nothing here read the constant: a number on the door that decides nothing
  // reads as a rule, so it is gone rather than kept for a later task.
  assert.equal('FIRST_FRAME_MS' in viewModule, false);
});

test('the newest document replaces the last one, and nothing is accumulated', () => {
  const slugA = 'example-one-0123456789abcdef';
  const slugB = 'example-two-0123456789abcdef';
  const first = document({
    repos: [
      { slug: slugA, counted_usd: 10, percent: 20, daily_cap_usd: 50 },
      { slug: slugB, counted_usd: 2.5, percent: 5, daily_cap_usd: 50 },
    ],
  });
  const second = document({ total: { counted_usd: 3, percent: 6 } });
  const view = new FeedView();
  assert.equal(view.ingest(first, { nowMs: T0 }).ok, true);
  assert.equal(view.snapshot({ nowMs: T0 }).drawable.repos.length, 2);
  view.ingest(second, { nowMs: T0 + 1_000 });

  const snapshot = view.snapshot({ nowMs: T0 + 1_000 });
  // The numbers are the second document's, not a sum of the two: a spend that
  // fell from 12.5 to 3 is 3, not 15.5.
  assert.equal(shown(snapshot.drawable.total.counted), '3');
  assert.equal(snapshot.drawable.total.percent.value, 6);
  // A shorter repository list replaces a longer one: the second document's
  // list is empty, and the first document's two entries are gone rather than
  // merged in.
  assert.deepEqual(snapshot.drawable.repos, []);
  // ... and the document itself is the one that arrived, by identity: the view
  // keeps the document, it does not build one.
  assert.equal(snapshot.document, second.document);
  assert.equal(snapshot.ingests, 2);
});

test('a changed day is a break, and the new day\'s zero is never drawn', () => {
  const dayOne = document({ day: '2025-11-09' });
  const view = shown_(dayOne, { nowMs: T0 });
  assert.equal(view.snapshot({ nowMs: T0 }).broken, false);
  assert.notEqual(view.snapshot({ nowMs: T0 }).drawable, null);

  // The rollover: the phone's day key has changed, it has subscribed to the new
  // day's paths, and nothing has been written there yet. The document carries
  // `counted_usd: 0` -- the CLI's own rollover document does (§10.7) -- and that
  // zero is a measured figure in the document.
  const dayTwoEmpty = document({ day: '2025-11-10', total: { counted_usd: 0, percent: 0 } });
  const changed = view.ingest(dayTwoEmpty, { nowMs: T0 + 1_000, measured: false });
  assert.equal(changed.dayChanged, true);
  const broken = view.snapshot({ nowMs: T0 + 1_000 });
  assert.equal(broken.dayChanged, true);
  assert.equal(broken.broken, true);
  // §10.7: "read a changed `day` as the design's gap state and break the series
  // there, and must not draw the zero". The zero is in the document and is not
  // drawable: `drawable` is null, so no screen can render it by accident.
  assert.equal(broken.drawable, null);
  assert.equal(shown(broken.document.total.counted), '0');
  assert.equal(broken.document.total.counted.state, STATE.MEASURED);

  // The new day's numbers follow once they are a measurement, and the break is
  // not carried past the document it happened on.
  const dayTwo = document({ day: '2025-11-10', total: { counted_usd: 0.5, percent: 1 } });
  view.ingest(dayTwo, { nowMs: T0 + 60_000 });
  const measured = view.snapshot({ nowMs: T0 + 60_000 });
  assert.equal(measured.dayChanged, false);
  assert.equal(measured.broken, false);
  assert.equal(shown(measured.drawable.total.counted), '0.5');
  assert.equal(measured.day, '2025-11-10');
});

test('a first document whose day has not delivered is a gap too', () => {
  // The same rule at the start of a session: the phone has just subscribed and
  // the day's counter node is not there yet. That is a gap, not a day of no
  // spend -- and it is the case a screen gets wrong by drawing what it has.
  const view = shown_(document(), { nowMs: T0, measured: false });
  const snapshot = view.snapshot({ nowMs: T0 });
  assert.equal(snapshot.broken, true);
  assert.equal(snapshot.drawable, null);
  assert.equal(snapshot.day, '2025-11-09');
  // ... and once the counter arrives, the same day is drawable with no break.
  view.ingest(document(), { nowMs: T0 + 1_000 });
  assert.equal(view.snapshot({ nowMs: T0 + 1_000 }).broken, false);
});

test('the degraded shape renders as degraded, never as a zero spend', () => {
  const view = new FeedView();
  const degraded = validateDocument({ project: 'fugaro', degraded: true, notice: 'no budget backend: showing run records only' });
  assert.equal(degraded.ok, true);
  view.ingest(degraded, { nowMs: T0 });
  const snapshot = view.snapshot({ nowMs: T0 });
  assert.equal(snapshot.degraded, true);
  assert.equal(snapshot.liveness, 'degraded');
  // Degraded is not broken: it is a different fact (the CLI has no budget
  // backend) and the screen shows its notice, not a gap in a series.
  assert.equal(snapshot.broken, false);
  // There are no figures to render, and nothing drawable: a zero spend would
  // have to be invented, which is the bug §2 (iii) names.
  assert.equal(snapshot.drawable, null);
  assert.equal(snapshot.day, null, 'the degraded document carries no day to place a series on');
  // The state survives a silence too, and it is a silence, not a death: past
  // `StaleAfter` the app says `stale`, and it keeps saying it however long the
  // quiet lasts, because that is as far as `State.conn` goes for a stream.
  assert.equal(view.snapshot({ nowMs: T0 + STALE_AFTER_MS + 1 }).liveness, 'stale');
  assert.equal(view.snapshot({ nowMs: T0 + VERY_QUIET_MS }).liveness, 'stale');

  // A real document after a degraded one is rendered as a real one: the view
  // holds the newest document, whatever shape it has. It describes the instant
  // it arrived at, so it is not a document about an old reading.
  view.ingest(document({ now: new Date(T0 + VERY_QUIET_MS + 2).toISOString() }), { nowMs: T0 + VERY_QUIET_MS + 2 });
  const recovered = view.snapshot({ nowMs: T0 + VERY_QUIET_MS + 2 });
  assert.equal(recovered.degraded, false);
  assert.equal(recovered.liveness, 'live');
  assert.equal(shown(recovered.drawable.total.counted), '12.5');
});

test('a feed that stops arriving is a silence, not a death', () => {
  const view = shown_(document(), { nowMs: T0 });
  assert.equal(view.snapshot({ nowMs: T0 }).liveness, 'live');
  assert.equal(view.snapshot({ nowMs: T0 + 1_000 }).liveness, 'live');
  // `watch.StaleAfter`, on the phone's own clock -- and the only threshold a
  // *stream* has (§10.9). The document's own state stays `live`, because the
  // document is what the reader knew when it last assembled one; the view's
  // liveness is the phone's own reading of the clock, and it is the worse of
  // the two.
  assert.equal(view.snapshot({ nowMs: T0 + STALE_AFTER_MS }).liveness, 'live', 'the boundary itself is not stale');
  assert.equal(view.snapshot({ nowMs: T0 + STALE_AFTER_MS + 1 }).liveness, 'stale');
  // ... and it stays `stale`: silence is not a state that keeps getting worse.
  // `State.conn` has no second threshold, and `watch.LostAfter` -- 180 s in
  // §10.9's table -- is a property of a *run* (`State.health` calls a run
  // `lost`), not of a connection. The phone reads no run, so a feed that has
  // been quiet for half an hour is still a feed that has been quiet.
  assert.equal(view.snapshot({ nowMs: T0 + 3 * 60_000 }).liveness, 'stale');
  assert.equal(view.snapshot({ nowMs: T0 + VERY_QUIET_MS }).liveness, 'stale');
  assert.equal(view.snapshot({ nowMs: T0 + VERY_QUIET_MS }).document.connection.state, 'live', 'the document is not re-assembled by a silence');

  // The last document's numbers are still there, and still measured: a silence
  // does not unmeasure a reading, it only says how long ago it was.
  const quiet = view.snapshot({ nowMs: T0 + 10 * 60_000 });
  assert.equal(shown(quiet.drawable.total.counted), '12.5');
  assert.equal(quiet.broken, false);
  assert.equal(quiet.silenceMs, 10 * 60_000);
  assert.equal(quiet.ageMs, 10 * 60_000);

  // The vocabulary has no word for a dead process, and never says the feed
  // ended: `dead`, `exited` and `stopped` are not states this app has.
  assert.deepEqual([...LIVENESS], ['no-document', 'live', 'stale', 'offline', 'refused', 'polling', 'degraded']);
  for (const state of LIVENESS) {
    assert.equal(/dead|exited|stopped|ended|killed/i.test(state), false, state);
  }
  // A feed that has never arrived is not offline and not dead: nothing has been
  // said yet.
  const empty = new FeedView();
  const nothing = empty.snapshot({ nowMs: T0 + 50_000 });
  assert.equal(nothing.liveness, 'no-document');
  assert.equal(nothing.document, null);
  assert.equal(nothing.drawable, null);
  assert.equal(nothing.silenceMs, null, 'there is no silence to measure before the first document');
});

test('the document\'s own state is the floor, and the phone\'s clock can only lower it', () => {
  // A document that says its streams are quiet is quiet even if it arrived a
  // moment ago, and `age_seconds` is the CLI's own measure of that.
  const staleDoc = document({ connection: { state: 'stale', reason: 'quiet', age_seconds: 50 } });
  const view = shown_(staleDoc, { nowMs: T0 });
  assert.equal(view.snapshot({ nowMs: T0 }).liveness, 'stale');
  assert.equal(view.snapshot({ nowMs: T0 }).silenceMs, 50_000);

  // A document that arrived promptly but describes an old instant is quiet too:
  // the rule names `now` as one of its four inputs. Old enough to be past
  // `StaleAfter` and not past anything else -- there is nothing else.
  const old = document({ now: new Date(T0 - 10 * 60_000).toISOString() });
  const late = shown_(old, { nowMs: T0 });
  assert.equal(late.snapshot({ nowMs: T0 }).liveness, 'stale');
  assert.equal(late.snapshot({ nowMs: T0 }).silenceMs, 10 * 60_000);

  // A refusal outranks the silence it caused: it is the diagnosis.
  const refused = shown_(document({ connection: { state: 'refused', reason: 'access refused', age_seconds: 0 } }), { nowMs: T0 });
  assert.equal(refused.snapshot({ nowMs: T0 }).liveness, 'refused');
  assert.equal(refused.snapshot({ nowMs: T0 + VERY_QUIET_MS }).liveness, 'refused');

  // Polling is a healthy way to read, not a fault (`render.go` treats it as
  // live), and it is reported as itself.
  const polling = shown_(document({ connection: { state: 'polling', age_seconds: 0 } }), { nowMs: T0 });
  assert.equal(polling.snapshot({ nowMs: T0 }).liveness, 'polling');
  const offline = shown_(document({ connection: { state: 'offline', reason: 'connecting', age_seconds: 0 } }), { nowMs: T0 });
  assert.equal(offline.snapshot({ nowMs: T0 }).liveness, 'offline');

  // A document whose instant is ahead of the phone's clock is not a negative
  // age, and the view does not call it a clock difference -- that is P4.2's.
  const ahead = shown_(document({ now: new Date(T0 + 60_000).toISOString() }), { nowMs: T0 });
  const skewed = ahead.snapshot({ nowMs: T0 });
  assert.equal(skewed.ageMs, 0);
  assert.equal(skewed.liveness, 'live');
});

test('a line that is not a document leaves the last one on screen, and says so', () => {
  const view = new FeedView();
  // Nothing has arrived yet and a line is skipped: still nothing, with a note.
  const skipped = decode('no budget backend: showing run records only');
  assert.equal(view.ingest(skipped, { nowMs: T0 }).ok, false);
  const first = view.snapshot({ nowMs: T0 });
  assert.equal(first.liveness, 'no-document');
  assert.equal(first.note, SKIP_NOTICE);
  // The notice never carries what was skipped.
  assert.equal(first.note.includes('budget'), false);

  view.ingest(document(), { nowMs: T0 });
  assert.equal(view.snapshot({ nowMs: T0 }).note, null, 'a document clears the note');
  view.ingest(skipped, { nowMs: T0 + 1_000 });
  const after = view.snapshot({ nowMs: T0 + 1_000 });
  assert.equal(after.note, SKIP_NOTICE);
  assert.equal(shown(after.drawable.total.counted), '12.5', 'a skipped line does not blank a measurement');
  assert.equal(after.ingests, 1, 'a skip is not a document');

  // A refusal is a different note, and the difference is the caller's to see.
  const refused = validateDocument({ project: 'fugaro', mode: 'observe', day: '2025-11-09', now: 'x', total: {} });
  assert.equal(refused.ok, false);
  view.ingest(refused, { nowMs: T0 + 2_000 });
  assert.equal(view.snapshot({ nowMs: T0 + 2_000 }).note, 'the feed sent something this app does not understand');
  // An empty `ingest` is not a document either, and does not throw.
  assert.equal(view.ingest(null, { nowMs: T0 }).ok, false);
  assert.equal(view.ingest(undefined, { nowMs: T0 }).ok, false);
});

test('what is drawable is always figures, and never a number nobody measured', () => {
  const view = shown_(document(), { nowMs: T0 });
  const snapshot = view.snapshot({ nowMs: T0 });
  assert.ok(snapshot.drawable.figures.length >= 8);
  for (const fig of snapshot.drawable.figures) assert.ok(isFigure(fig), JSON.stringify(fig));
  // A snapshot is frozen, and so is what it hands out.
  assert.ok(Object.isFrozen(snapshot));
  assert.ok(Object.isFrozen(snapshot.drawable));
  // The same call twice is the same answer: a snapshot has no clock of its own.
  assert.deepEqual(view.snapshot({ nowMs: T0 }), snapshot);
});
