// The reader, and the grant's fence (mobile plan, Task P3.2; D112's answer,
// tension (b1)).
//
// **The plan's added test case is the first one here**: "no request is ever
// made for `agents/**` or `outcomes/**`". It is a statement about requests, so
// it is proved the only way a request can be proved absent: the port below
// **records every path it is handed**, and the assertion is on that record.
// A reader that asked for a denied path and handled the refusal would pass a
// test that only checked the refusal, and fail this one.
//
// The rest of the file is the reader's own jobs: the plan is the granted shapes
// and nothing else, a listener that cannot be registered is a start that stops,
// a switch tears the listeners down, the day rollover re-subscribes and breaks
// the series, and the connection is the worst of the granted paths -- the same
// rule `State.conn` applies to its four streams.
//
// No child process and no network: the port is a fake, and every value in the
// test is a recorded node.

import assert from 'node:assert/strict';
import test from 'node:test';

import { ResourceRegistry } from '../accounts/resources.js';
import { FIXTURE_SLUG, FIXTURE_UID, recordedNodes, recordedPaths } from './fixtures/nodes.js';
import { MICROS_PER_USD, UNREADABLE_KILL } from './nodes.js';
import { FeedReader, MAX_PATHS, READER_CODES, isPermissionError } from './reader.js';
import { dayDate, dayKey } from './paths.js';
import { BURN_WARMUP_MS } from './recompute.js';
import { STALE_AFTER_MS } from './view.js';

const UID = FIXTURE_UID;
const SLUG = FIXTURE_SLUG;
const T0 = Date.parse('2025-11-09T12:00:00Z');
const DAY = dayKey(T0);
const ACCOUNT = Object.freeze({
  id: 'acct-1',
  firebaseProjectId: 'fugaro-dev',
  authUid: UID,
  fugaroProject: 'fugaro',
});

/**
 * A port that records every path it is asked for and can deliver values.
 *
 * It is the seam the plan's test case needs: `requests` is the list of paths
 * the reader actually handed to the database layer, and it is a list a denied
 * path would appear in.
 */
class FakePort {
  requests = [];
  listeners = new Map();
  stopped = [];
  polling = false;
  failSubscribe = false;
  /** The instant the reader's injected clock reads. A delivery moves it to the
   * instant the delivery happened at, which is what a real port would do by
   * delivering at the moment it delivers. */
  nowMs = T0;

  subscribe(path, { onValue, onError }) {
    this.requests.push(path);
    if (this.failSubscribe) return null;
    const handle = () => this.stopped.push(path);
    this.listeners.set(path, { onValue, onError });
    return handle;
  }

  /** Deliver a node value to whoever subscribed to its path. */
  put(path, value, nowMs = this.nowMs) {
    const listener = this.listeners.get(path);
    assert.ok(listener !== undefined, `nothing subscribed to ${path}`);
    this.nowMs = nowMs;
    listener.onValue(value, nowMs);
  }

  /** Answer every subscribed path with the same value, as a fresh subscription
   * does in the Realtime Database (the current value arrives at once). */
  flush(valueFor, nowMs = this.nowMs) {
    for (const [path, listener] of [...this.listeners]) {
      this.nowMs = nowMs;
      listener.onValue(valueFor(path), nowMs);
    }
  }
}

function reader({ port = new FakePort(), registry = new ResourceRegistry(), open = true, repositories = [SLUG], account = ACCOUNT, now = () => port.nowMs } = {}) {
  if (open) registry.open(account.id);
  return { reader: new FeedReader({ port, account, repositories, registry, now }), port, registry };
}

/** The recorded nodes a healthy account has: caps, a day counter, and no kill
 * switch (`fixtures/nodes.js`). A path the recording does not cover is the
 * database's own `null` -- a node that is not there. */
const RECORDED = recordedNodes({ day: DAY, atMs: T0 });
const healthy = (path) => RECORDED[path] ?? null;

test('no request is ever made for a denied family, and the port proves it', () => {
  const { reader: feed, port } = reader();
  const started = feed.start(T0);
  assert.equal(started.ok, true, JSON.stringify(started));

  // The whole record of what this reader asked the database for.
  assert.ok(port.requests.length > 0, 'the reader asked for something');
  for (const path of port.requests) {
    assert.equal(/^agents\//.test(path), false, path);
    assert.equal(/^outcomes\//.test(path), false, path);
    assert.equal(/^runs\//.test(path), false, path);
    assert.equal(/^fugaro\//.test(path), false, path);
    assert.equal(/\/runs\//.test(path), false, path);
    assert.equal(/inbox/.test(path), false, path);
    // ... and every request is one of the four granted families, by shape.
    assert.ok(
      /^(config\/caps\/(global|defaults|repos\/[a-z0-9-]{1,63})|config\/kill\/(global|repos\/[a-z0-9-]{1,63})|spend\/[0-9]+\/(global|repos\/[a-z0-9-]{1,63})|mailbox\/[A-Za-z0-9]{1,128}\/(outbox|heartbeat))$/.test(path),
      path,
    );
  }
  // The exact plan: the caps, the kill switches, the day's spend, the mailbox.
  assert.deepEqual(port.requests, [...recordedPaths({ day: DAY })]);
  // Asking what would be requested asks the port nothing.
  const before = port.requests.length;
  assert.equal(feed.plan(T0).ok, true);
  assert.equal(port.requests.length, before);
});

test('the plan is the granted shapes, and a bad repository list is refused before any request', () => {
  const { reader: feed, port } = reader({ repositories: ['Example', SLUG, SLUG] });
  const planned = feed.plan(T0);
  assert.equal(planned.ok, false);
  assert.equal(planned.code, READER_CODES.BAD_REPOSITORIES);
  assert.equal(feed.start(T0).ok, false);
  assert.deepEqual(port.requests, [], 'a refused plan asks the database for nothing');

  // A repository list is owner-typed, and it is bounded: every entry is a
  // listener, so a list that grew without bound is a bound on sockets.
  const many = Array.from({ length: MAX_PATHS }, (_, i) => `repo-${i}`);
  assert.equal(reader({ repositories: many }).reader.plan(T0).code, READER_CODES.TOO_MANY_PATHS);

  // A clock that is not an instant is refused rather than defaulted.
  assert.equal(reader().reader.plan('now').code, READER_CODES.BAD_NOW);
  assert.equal(reader().reader.plan(undefined).code, READER_CODES.BAD_NOW);

  // An account without a usable uid is refused before a path is composed: the
  // uid is what names the mailbox, and `mailbox/undefined/outbox` is a path
  // this app made up rather than one the grant names.
  const { reader: noAccount, port: accountPort } = reader({ account: { id: 'not an id' }, open: false });
  assert.equal(noAccount.plan(T0).code, READER_CODES.BAD_ACCOUNT);
  assert.equal(noAccount.start(T0).code, READER_CODES.BAD_ACCOUNT);
  assert.deepEqual(accountPort.requests, []);
});

test('a value for a path the reader never asked for is refused and not stored', () => {
  const { reader: feed } = reader();
  feed.start(T0);
  for (const path of ['agents/example-one-0123456789abcdef/run-1', 'outcomes/20401/x/y', 'runs/a/b', 'config/mode']) {
    const judged = feed.ingest(path, { counted: 1 }, T0);
    assert.equal(judged.ok, false, path);
    assert.equal(judged.code, READER_CODES.NOT_GRANTED, path);
  }
  // Nothing changed: the document is still the one the granted nodes add up to.
  feed.ingest(`spend/${DAY}/global`, { counted: 1_000_000 }, T0);
  assert.equal(feed.snapshot({ nowMs: T0 }).document.total.counted.value, 1);
});

test('the listener belongs to the account, and a switch stops every one of them', () => {
  const registry = new ResourceRegistry();
  const { reader: feed, port } = reader({ registry });
  const started = feed.start(T0);
  assert.equal(started.ok, true);
  assert.equal(started.listeners, 9);
  // Every listener is registered with the account, so a switch tears the feed
  // down with the rest of the account (D114's "no cross-leakage").
  assert.equal(registry.liveCount(ACCOUNT.id), 9);
  const closed = registry.close(ACCOUNT.id);
  assert.equal(closed.ok, true);
  assert.equal(closed.stopped, 9);
  assert.equal(port.stopped.length, 9);
  assert.equal(registry.liveCount(ACCOUNT.id), 0);

  // A reader whose account is not open cannot subscribe at all: nothing would
  // ever stop the listener, so not one is opened.
  const { reader: unopened, port: unopenedPort } = reader({ open: false });
  const refused = unopened.start(T0);
  assert.equal(refused.ok, false);
  assert.equal(refused.code, READER_CODES.NOT_OPEN);
  assert.deepEqual(unopenedPort.requests, [], 'not one socket is opened for a closed account');

  // A port that is not a port, and a port that cannot subscribe, are two
  // different refusals -- neither is a half-subscribed reader.
  const { reader: noPort } = reader({ port: null });
  assert.equal(noPort.start(T0).code, READER_CODES.BAD_PORT);
  const broken = new FakePort();
  broken.failSubscribe = true;
  const { reader: half } = reader({ port: broken });
  assert.equal(half.start(T0).code, READER_CODES.BAD_LISTENER);
  // ... and starting twice is refused rather than subscribed twice.
  const { reader: once } = reader();
  once.start(T0);
  assert.equal(once.start(T0).code, READER_CODES.ALREADY_STARTED);
});

test('the granted nodes add up to a document the contract\'s rules accept', () => {
  const { reader: feed, port } = reader();
  feed.start(T0);
  port.flush(healthy);
  const snapshot = feed.snapshot({ nowMs: T0 });
  const total = snapshot.document.total;
  assert.equal(total.counted.value, 12.5);
  assert.equal(total.spent.value, 12);
  assert.equal(total.notional.value, 40);
  assert.equal(total.notional.state, 'notional', 'a list price is never a bill');
  // The project's cap is `/config/caps/global`; the repository's is its own.
  assert.equal(total.dailyCap.value, 50);
  assert.equal(total.percent.value, 25);
  assert.equal(snapshot.document.repos[0].dailyCap.value, 5);
  assert.equal(snapshot.document.repos[0].percent.value, 60);
  // The mode is not in the grant, so the phone reports none rather than
  // guessing `observe` (`document.js`).
  assert.equal(snapshot.document.mode, null);
  assert.equal(snapshot.document.project, 'fugaro');
  assert.equal(snapshot.day, dayDate(DAY));
  assert.equal(snapshot.document.day, dayDate(DAY));
  // No kill switch anywhere: not killed is a fact, not a missing value.
  assert.equal(total.kill, null);
  // The runs bucket is not the phone's, and the document carries no rows.
  assert.equal(total.runs.value, null);
  assert.equal(total.runs.state, 'unavailable');
  // ... and the repository name is not read from the tree either: the node
  // carries `repo`, which is free text and never names a block.
  assert.equal(snapshot.document.repos[0].repo, null);
});

test('a missing node is unavailable, never a zero, in what the phone assembles', () => {
  const { reader: feed, port } = reader();
  feed.start(T0);
  // The database answers every subscribed path with nothing at all.
  port.flush(() => null);
  const snapshot = feed.snapshot({ nowMs: T0 });
  const total = snapshot.document.total;
  // The day counted nothing: `Counters` is a plain field with `omitempty`, so
  // an absent counter node is a measured zero, exactly as the CLI reads it.
  assert.equal(total.counted.value, 0);
  assert.equal(total.counted.state, 'measured');
  // The caps are not set, and an unset cap is `unavailable` -- never a cap of
  // zero, which would be a project allowed to spend nothing.
  assert.equal(total.dailyCap.value, null);
  assert.equal(total.dailyCap.state, 'unavailable');
  assert.equal(snapshot.document.repos[0].dailyCap.value, null);
  // ... and so the fraction it would have been is unavailable too, not 0%.
  assert.equal(total.percent.value, null);
  assert.equal(snapshot.document.repos[0].percent.value, null);
  // The rate has no samples, so it is unknown rather than zero.
  assert.equal(total.burn.known, false);
  assert.equal(total.burn.usdPerMin.value, null);
});

test('the burn window is fed spent + notional, exactly as State.observe feeds it', () => {
  const { reader: feed, port } = reader();
  feed.start(T0);
  port.flush(healthy);
  // The recording's global node is 12 USD spent and 40 USD notional, so the
  // first sample is **52** -- not the 12.5 `counted` the bar divides by the cap.
  assert.equal(feed.snapshot({ nowMs: T0 }).document.total.burn.known, false);

  // **The counted total moving on its own is not a rate.** `counted` jumps by
  // 5 USD here while `spent + notional` is unchanged at 52: a window fed
  // `counted` would report a slope and call it fast, and the console -- reading
  // the same tree -- would report no slope at all. The bar, which *is* over
  // counted, moves.
  port.put(`spend/${DAY}/global`, { counted: 17_500_000, spent: 12_000_000, notional: 40 * MICROS_PER_USD }, T0 + 120_000);
  const flat = feed.snapshot({ nowMs: T0 + 120_000 }).document.total;
  assert.equal(flat.burn.known, true, 'the window spans the warmup, so the rate is known');
  assert.equal(flat.burn.usdPerMin.value, 0, 'spent + notional is where it was');
  assert.equal(flat.burn.fast, false);
  assert.equal(flat.percent.value, 35, 'the bar is over counted, and counted did move');

  // ... and the same reading with the spend moved: 15 USD over the window's
  // 180 s is 5 USD/min, which is 300 USD/h against the 6.25 threshold.
  port.put(`spend/${DAY}/global`, { counted: 17_500_000, spent: 27_000_000, notional: 40 * MICROS_PER_USD }, T0 + 180_000);
  const fast = feed.snapshot({ nowMs: T0 + 180_000 }).document.total;
  assert.ok(Math.abs(fast.burn.usdPerMin.value - 5) < 1e-9, String(fast.burn.usdPerMin.value));
  assert.equal(fast.burn.fast, true);
});

test('a counter that goes backwards clears the window, as State.record does', () => {
  const { reader: feed, port } = reader();
  feed.start(T0);
  port.flush(healthy);
  // Two readings that give a slope...
  port.put(`spend/${DAY}/global`, { spent: 12_040_000, notional: 40 * MICROS_PER_USD }, T0 + 120_000);
  assert.equal(feed.snapshot({ nowMs: T0 + 120_000 }).document.total.burn.known, true);

  // ... and then the counter goes **back**: a rollover or a reset. The window
  // starts again from this reading, so the rate is unknown until it spans the
  // warmup -- not the slope of the series that ended, and never negative.
  port.put(`spend/${DAY}/global`, { spent: 1_000_000, notional: 0 }, T0 + 180_000);
  const reset = feed.snapshot({ nowMs: T0 + 180_000 }).document.total;
  assert.equal(reset.burn.known, false);
  assert.equal(reset.burn.usdPerMin.value, null);
  assert.equal(reset.burn.fast, false);
  // ... and it comes back from the new reading, once the window has a span.
  port.put(`spend/${DAY}/global`, { spent: 2_000_000, notional: 0 }, T0 + 180_000 + BURN_WARMUP_MS);
  const warm = feed.snapshot({ nowMs: T0 + 180_000 + BURN_WARMUP_MS }).document.total;
  assert.equal(warm.burn.known, true);
  assert.equal(warm.burn.usdPerMin.value, 1, '1 USD over the 60 s warmup');
});

test('a changed day is a break, and the new day is re-subscribed', () => {
  const { reader: feed, port } = reader();
  feed.start(T0);
  port.flush(healthy);
  assert.equal(feed.snapshot({ nowMs: T0 }).broken, false);
  const before = port.requests.length;

  // The clock crosses UTC midnight. Nothing has changed on the phone except the
  // day it is in.
  const tomorrow = T0 + 86_400_000;
  const nextDay = dayKey(tomorrow);
  const refreshed = feed.refresh(tomorrow);
  assert.equal(refreshed.ok, true);
  assert.equal(refreshed.dayChanged, true);
  assert.equal(feed.day(), dayDate(nextDay));
  // The old day's spend paths are gone and the new day's are asked for; the
  // caps, the kill switches and the mailbox are the same nodes and are not
  // re-subscribed.
  assert.equal(port.requests.length, before + 2);
  assert.deepEqual(port.requests.slice(before), [`spend/${nextDay}/global`, `spend/${nextDay}/repos/${SLUG}`]);
  assert.equal(port.stopped.length, 2, 'the old day\'s listeners are stopped, not left alive');

  // §10.7: the day the clock moved into is a gap until its counter is heard.
  // The values the reader still holds are the caps and the kill switches, so
  // the assembled document carries the new day and a zero spend.
  port.put(`config/caps/global`, { dailyMicros: 50 * MICROS_PER_USD }, tomorrow);
  const gap = feed.snapshot({ nowMs: tomorrow });
  assert.equal(gap.dayChanged, true);
  assert.equal(gap.broken, true);
  assert.equal(gap.drawable, null, 'the zero the rollover document carries is not drawable');
  assert.equal(gap.document.total.counted.value, 0);

  // The new day's counter is heard: the series is real again, and the burn
  // window restarted with the day (`SetDay` empties it), so the rate is unknown
  // rather than zero for its warmup.
  port.put(`spend/${nextDay}/global`, { counted: 250_000, spent: 250_000 }, tomorrow + 1_000);
  const measured = feed.snapshot({ nowMs: tomorrow + 1_000 });
  assert.equal(measured.broken, false);
  assert.equal(measured.dayChanged, false);
  assert.equal(measured.drawable.total.counted.value, 0.25);
  assert.equal(measured.document.total.burn.known, false);
  assert.equal(measured.document.total.burn.usdPerMin.value, null);
  // ... and it comes back once the new window has a span.
  port.put(`spend/${nextDay}/global`, { counted: 310_000, spent: 310_000 }, tomorrow + BURN_WARMUP_MS + 1_000);
  const warm = feed.snapshot({ nowMs: tomorrow + BURN_WARMUP_MS + 1_000 }).document.total;
  assert.equal(warm.burn.known, true);
  assert.ok(Math.abs(warm.burn.usdPerMin.value - 0.06) < 1e-9);
  // Refreshing inside the same day changes nothing at all.
  const same = feed.refresh(tomorrow + 2_000);
  assert.equal(same.dayChanged, false);
  assert.equal(port.requests.length, before + 2);
});

test('the connection is the worst of the granted paths, and never says the feed died', () => {
  const { reader: feed, port } = reader();
  feed.start(T0);
  // Nothing heard yet: the CLI's own "connecting" is an offline state, and it
  // is not a death.
  assert.equal(feed.snapshot({ nowMs: T0 }).document.connection.state, 'offline');
  assert.equal(feed.snapshot({ nowMs: T0 }).document.connection.reason, 'connecting');
  assert.equal(feed.snapshot({ nowMs: T0 }).liveness, 'offline');

  port.flush(healthy);
  assert.equal(feed.snapshot({ nowMs: T0 }).document.connection.state, 'live');
  assert.equal(feed.snapshot({ nowMs: T0 }).liveness, 'live');
  // The document's own `connection` is what the reader knew when it last
  // assembled one -- a quiet feed assembles nothing new, so the state on the
  // document stays where it was while the **view's** liveness, which is the
  // phone's own reading of the clock, goes stale. That is the whole of "silence
  // is not death": the numbers stay, and the app says how long it has been
  // quiet -- and it says `stale`, however long, because `State.conn` has no
  // threshold past `StaleAfter` and `watch.LostAfter` is a *run*'s (the phone
  // reads no run).
  assert.equal(feed.snapshot({ nowMs: T0 + STALE_AFTER_MS + 1 }).document.connection.state, 'live');
  assert.equal(feed.snapshot({ nowMs: T0 + STALE_AFTER_MS + 1 }).liveness, 'stale');
  assert.equal(feed.snapshot({ nowMs: T0 + 30 * 60_000 }).liveness, 'stale');
  // The reader's own connection does not go `offline` for a silence either: it
  // assembles nothing new, so the state it last wrote is still `live`.
  assert.equal(feed.snapshot({ nowMs: T0 + 30 * 60_000 }).document.connection.state, 'live');

  // The oldest node is the one that decides: one node answering a minute later
  // does not make the feed fresh, because the day's counter is still a minute
  // old. `age_seconds` is the CLI's own measure of that.
  port.put(`mailbox/${UID}/heartbeat`, { at: T0 + 60_000, boot: 'b1' }, T0 + 60_000);
  const quiet = feed.snapshot({ nowMs: T0 + 60_000 }).document.connection;
  assert.equal(quiet.ageSeconds, 60, 'the wire spells it `age_seconds`; the judged document is this app\'s own shape');
  assert.equal(quiet.state, 'stale');

  // A refused read is a diagnosis, and it outranks the silence it causes. The
  // reason is this app's own sentence, never the database's.
  port.listeners.get(`spend/${DAY}/global`).onError({ code: 'PERMISSION_DENIED', message: 'Permission denied: /spend/20401/global' });
  const refused = feed.snapshot({ nowMs: T0 }).document.connection;
  assert.equal(refused.state, 'refused');
  assert.equal(refused.reason, 'access refused');
  assert.equal(JSON.stringify(refused).includes('permission'), false);
  // ... and the path answering again clears it.
  port.put(`spend/${DAY}/global`, { counted: 1 }, T0);
  assert.equal(feed.snapshot({ nowMs: T0 }).document.connection.state, 'live');

  // Polling is a way to read, not a fault.
  port.polling = true;
  port.flush(healthy);
  assert.equal(feed.snapshot({ nowMs: T0 }).document.connection.state, 'polling');
  assert.equal(feed.snapshot({ nowMs: T0 }).liveness, 'polling');
  // The vocabulary still has no word for a dead feed.
  for (const state of ['live', 'stale', 'offline', 'refused', 'polling', 'degraded', 'no-document']) {
    assert.equal(/dead|exited|stopped/i.test(state), false, state);
  }
});

test('a kill switch the phone cannot read is a kill switch that is on', () => {
  const { reader: feed, port } = reader();
  feed.start(T0);
  port.flush(healthy);
  assert.equal(feed.snapshot({ nowMs: T0 }).document.total.kill, null);
  // A node that is there but is not a switch: `State.kill`'s unreadable case.
  // The **document's** kill carries no `on` (presence *is* the on-ness, §2, and
  // `jsonKill` returns nil when the switch is off), so what the wire document
  // carries is the three fields -- and what a judged document carries is the
  // same three with `on: true` put back by `document.js`.
  port.put('config/kill/global', 'true', T0);
  assert.deepEqual(feed.snapshot({ nowMs: T0 }).document.total.kill, { on: true, by: null, at: null, reason: UNREADABLE_KILL });
  // ... and a real one, thrown at an instant in milliseconds.
  port.put('config/kill/global', { on: true, by: 'operator', at: T0, reason: 'cap' }, T0);
  assert.deepEqual(feed.snapshot({ nowMs: T0 }).document.total.kill, {
    on: true,
    by: 'operator',
    at: new Date(T0).toISOString(),
    reason: 'cap',
  });
  // A repository's own switch, and one that is off.
  port.put(`config/kill/repos/${SLUG}`, { on: false, by: 'operator' }, T0);
  assert.equal(feed.snapshot({ nowMs: T0 }).document.repos[0].kill, null);
});

test('a node that cannot be read is unavailable, and never a zero', () => {
  const { reader: feed, port } = reader();
  feed.start(T0);
  port.flush(healthy);
  assert.equal(feed.snapshot({ nowMs: T0 }).drawable.total.counted.value, 12.5);
  // A counter node holding text. `readCounters` refuses it -- it is not a
  // counter -- and the day's figure is `unavailable`, **never** a zero, which
  // would say the day has spent nothing (§10.13 rule 5).
  const judged = feed.ingest(`spend/${DAY}/global`, 'not a document', T0 + 1_000);
  assert.equal(judged.ok, true);
  const snapshot = feed.snapshot({ nowMs: T0 + 1_000 });
  const total = snapshot.drawable.total;
  assert.equal(total.counted.state, 'unavailable');
  assert.equal(total.counted.value, null);
  assert.notEqual(total.counted.value, 0);
  assert.equal(total.spent.value, null, 'the whole node was replaced, and none of it is readable');
  // One unreadable node does not blank the screen: the rest of the document is
  // still the last measurement, and the day is still the day it was.
  assert.equal(snapshot.broken, false);
  assert.equal(snapshot.drawable.repos[0].counted.value, 3);
  assert.equal(snapshot.day, dayDate(DAY));
  // ... and a value that was not a reading is not a sample either: the burn
  // window is untouched, so the rate is still the last one it could measure.
  assert.equal(feed.snapshot({ nowMs: T0 + 1_000 }).document.total.burn.known, false);
  // A node that *is* an object and merely lacks a field is not this case: an
  // absent counter is a measured zero, exactly as the CLI reads it.
  port.put(`spend/${DAY}/global`, { calls: 2 }, T0 + 2_000);
  const empty = feed.snapshot({ nowMs: T0 + 2_000 }).drawable.total;
  assert.equal(empty.counted.value, 0);
  assert.equal(empty.counted.state, 'measured');
});

test('a reader that was never started refuses to refresh, and stop is final', () => {
  const { reader: feed, port } = reader();
  assert.equal(feed.refresh(T0).code, READER_CODES.NOT_STARTED);
  assert.equal(feed.day(), null);
  assert.equal(feed.snapshot({ nowMs: T0 }).liveness, 'no-document');
  feed.start(T0);
  assert.equal(feed.day(), dayDate(DAY));
  feed.stop();
  assert.equal(feed.snapshot({ nowMs: T0 }).paths.length, 0);
  assert.equal(port.stopped.length, 9);
  assert.equal(feed.refresh(T0 + 86_400_000).code, READER_CODES.NOT_STARTED);
});

test('a permission error is refused, and a failure to connect is not', () => {
  // The distinction `State.conn` makes, and the one a phone gets wrong: it
  // matches `errors.Is(err, rtdb.ErrPermission)` for the refusal and treats
  // **every** other error as a connection that is not up. A port hands over the
  // error it got; this is where the judgement is made.
  assert.equal(isPermissionError({ code: 'PERMISSION_DENIED' }), true);
  assert.equal(isPermissionError({ code: 'permission-denied' }), true);
  assert.equal(isPermissionError({ code: ' PERMISSION_DENIED ' }), true);
  assert.equal(isPermissionError({ code: 'UNAVAILABLE' }), false);
  assert.equal(isPermissionError(new Error('permission denied')), false, 'a message is not a code');
  assert.equal(isPermissionError({ message: 'Permission denied' }), false);
  assert.equal(isPermissionError(null), false);
  assert.equal(isPermissionError('PERMISSION_DENIED'), false);

  const { reader: feed, port } = reader();
  feed.start(T0);
  port.flush(healthy);
  const path = `spend/${DAY}/global`;
  const other = `spend/${DAY}/repos/${SLUG}`;

  // A dropped socket: the feed is not up, and it is **not** an access problem.
  port.listeners.get(path).onError({ code: 'UNAVAILABLE', message: 'network error' });
  const failed = feed.snapshot({ nowMs: T0 }).document.connection;
  assert.equal(failed.state, 'offline');
  assert.equal(failed.reason, 'the read failed');
  assert.equal(JSON.stringify(failed).includes('network'), false, 'the port\'s message is not this app\'s sentence');

  // ... and a refusal is a different state, which is the whole point.
  port.listeners.get(path).onError({ code: 'PERMISSION_DENIED' });
  assert.equal(feed.snapshot({ nowMs: T0 }).document.connection.state, 'refused');
  // A refusal outranks a failure when both are on the wire: `State.conn` asks
  // for the permission error first.
  port.listeners.get(other).onError({ code: 'UNAVAILABLE' });
  assert.equal(feed.snapshot({ nowMs: T0 }).document.connection.state, 'refused');
  // Each path clears its own when it delivers again.
  port.put(path, { counted: 1 }, T0);
  assert.equal(feed.snapshot({ nowMs: T0 }).document.connection.state, 'offline', 'the other path has still failed');
  port.put(other, { counted: 1 }, T0);
  assert.equal(feed.snapshot({ nowMs: T0 }).document.connection.state, 'live');
  // A path the reader never asked for is not a stream it can break.
  assert.equal(feed.failed('agents/example-one-0123456789abcdef/run-1', { code: 'PERMISSION_DENIED' }, T0).code, READER_CODES.NOT_GRANTED);
  assert.equal(feed.failed('agents/example-one-0123456789abcdef/run-1', { code: 'UNAVAILABLE' }, T0).code, READER_CODES.NOT_GRANTED);
});

test('the account is judged as the account module judges it, so no uid is ever made up', () => {
  // The reader composes `mailbox/<uid>/outbox` out of the account, and a uid it
  // never judged is a path it made up: `mailbox/undefined/outbox` has exactly
  // the shape the gate grants, so a reader that judged only the account id
  // would **subscribe to it** -- the request the plan's own case forbids.
  const variants = [
    { id: 'acct-1', firebaseProjectId: 'fugaro-dev', fugaroProject: 'fugaro' },
    { id: 'acct-1', firebaseProjectId: 'fugaro-dev', authUid: undefined, fugaroProject: 'fugaro' },
    { id: 'acct-1', firebaseProjectId: 'fugaro-dev', authUid: 42, fugaroProject: 'fugaro' },
    { id: 'acct-1', firebaseProjectId: 'fugaro-dev', authUid: 'a uid with spaces', fugaroProject: 'fugaro' },
    { id: 'acct-1', firebaseProjectId: 'fugaro-dev', authUid: '', fugaroProject: 'fugaro' },
  ];
  for (const account of variants) {
    const { reader: feed, port } = reader({ account });
    const planned = feed.plan(T0);
    assert.equal(planned.ok, false, JSON.stringify(account));
    assert.equal(planned.code, READER_CODES.BAD_ACCOUNT);
    assert.equal(planned.field, 'authUid', JSON.stringify(account));
    assert.equal(feed.start(T0).code, READER_CODES.BAD_ACCOUNT);
    assert.deepEqual(port.requests, [], 'nothing is asked for an account that was never judged');
  }
  // The same account with a uid the module accepts is planned, so the refusal
  // above is about the uid and not about the shape of the test's account.
  assert.equal(reader().reader.plan(T0).ok, true);
});

test('a clock that is not an instant is refused at every entry point', () => {
  const { reader: feed, port } = reader();
  // `new Date(1e300).toISOString()` **throws**: a finite number is not
  // necessarily an instant, and a clock that cannot be rendered cannot be a
  // document's `now`.
  for (const bad of [undefined, null, 'now', NaN, Infinity, 1e300, -1e300]) {
    assert.equal(feed.plan(bad).code, READER_CODES.BAD_NOW, String(bad));
  }
  assert.equal(feed.start(1e300).code, READER_CODES.BAD_NOW);
  assert.deepEqual(port.requests, [], 'a clock that is not an instant asks for nothing');

  assert.equal(feed.start(T0).ok, true);
  // Every entry point judges it the same way, so there is no door left for an
  // unrenderable instant to reach an assembly through.
  assert.equal(feed.ingest(`spend/${DAY}/global`, { counted: 1 }, 1e300).code, READER_CODES.BAD_NOW);
  assert.equal(feed.refresh(1e300).code, READER_CODES.BAD_NOW);
  assert.equal(feed.refresh(undefined).code, READER_CODES.BAD_NOW);
  // ... and a clock that is one still works.
  assert.equal(feed.refresh(T0).ok, true);
  assert.equal(feed.ingest(`spend/${DAY}/global`, { counted: 1 }, T0).ok, true);

  // A **broken** clock is not a reason to throw. A port's error arrives with no
  // instant of its own, so the reader asks its clock -- and an assembly from a
  // clock that cannot be rendered refuses rather than raising inside itself.
  const { reader: broken } = reader({ now: () => 1e300 });
  assert.equal(broken.start(T0).ok, true, 'the instant a caller passes is the one that is judged');
  assert.equal(broken.refused(`spend/${DAY}/global`).code, READER_CODES.BAD_NOW);
});

test('stop forgets the session, and a restart does not inherit it', () => {
  const { reader: feed, port } = reader();
  feed.start(T0);
  port.flush(healthy);
  assert.equal(feed.snapshot({ nowMs: T0 }).drawable.total.counted.value, 12.5);
  assert.equal(feed.day(), dayDate(DAY));

  feed.stop();
  assert.equal(feed.day(), null, 'a stopped reader is subscribed to no day');
  assert.equal(feed.snapshot({ nowMs: T0 }).paths.length, 0);

  // A restart re-plans and re-subscribes, and it starts from **nothing**: the
  // previous session's nodes are not this session's deliveries, so the day is
  // not measured until the new listeners have answered -- §10.7's gap, and not
  // a measurement from a session that has ended.
  assert.equal(feed.start(T0).ok, true);
  assert.deepEqual(port.requests.slice(-9), [...recordedPaths({ day: DAY })], 'the whole plan is asked for again');
  const fresh = feed.snapshot({ nowMs: T0 });
  assert.equal(fresh.broken, true, 'the old session\'s counters are not freshly delivered');
  assert.equal(fresh.drawable, null);
  assert.equal(fresh.document.total.counted.value, 0, 'the node is not there, and an absent counter is a measured zero');
  assert.equal(fresh.document.total.burn.known, false, 'the burn window went with the session');

  // ... and the new listeners make it real again.
  port.flush(healthy);
  const again = feed.snapshot({ nowMs: T0 });
  assert.equal(again.broken, false);
  assert.equal(again.drawable.total.counted.value, 12.5);
});

test('a kill switch whose instant this app cannot render does not kill the feed', () => {
  const { reader: feed, port } = reader();
  feed.start(T0);
  port.flush(healthy);
  // A nanosecond timestamp: an `int64` Go reads happily (and prints as a year
  // far past any calendar), and an instant `new Date(...).toISOString()`
  // **throws** on. A reader that let it through would throw while assembling a
  // document from a node it had already stored, so every later assembly would
  // throw too: the feed would be dead for the rest of the session, and a
  // switch that is ON would read as off.
  port.put('config/kill/global', { on: true, by: 'operator', at: 1_700_000_000_000_000_000, reason: 'cap' }, T0);
  const killed = feed.snapshot({ nowMs: T0 });
  assert.deepEqual(killed.document.total.kill, { on: true, by: null, at: null, reason: UNREADABLE_KILL });

  // The feed is alive: the next delivery still assembles, and the switch is
  // still believed.
  port.put(`spend/${DAY}/global`, { counted: 2_000_000 }, T0 + 1_000);
  const next = feed.snapshot({ nowMs: T0 + 1_000 });
  assert.equal(next.drawable.total.counted.value, 2);
  assert.equal(next.document.total.kill.on, true, 'a switch this app cannot read is a switch that is on');
});
