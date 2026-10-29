// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The list, and the wiring (mobile plan, Task P3.3; resolved 2026-10-17).
//
// Two halves. The first is `jobList`'s judgement of what it is handed -- a
// snapshot that is not the feed's is refused, and a broken or degraded series
// draws no row. The second is the end-to-end one, and it is the reason this
// file stands the reader up: the list is built through `createJobList` over a
// **fake port and P3.2's recorded nodes**, so "a recomputed burn rate or cap
// fraction is correct against a fixture" and "a changed `day` is a break" are
// asserted over the wiring the app will actually use, with no child process and
// no network (Global Constraint 10).
//
// The port records every path it is handed (P3.2's own device for proving a
// request was never made), so the end-to-end half also pins this module's side
// of the grant: the wiring asks for the four families and nothing else.

import assert from 'node:assert/strict';
import test from 'node:test';

import { ResourceRegistry } from '../accounts/resources.js';
import { FIXTURE_SLUG, FIXTURE_UID, recordedNodes } from '../feed/fixtures/nodes.js';
import { FAMILIES, GAP_MARK, dayDate, dayKey, familyOf, validateDocument } from '../feed/index.js';
// The gate itself, from its own module: the door carries the families a screen
// may name, and the gate is the feed's business (this is a test file, which is
// what `scope.test.js` exempts from the door rule).
import { DENIED_FAMILIES, request } from '../feed/paths.js';
import { LIST_CODES } from './fields.js';
import { JobList, createJobList, jobList } from './list.js';
import { lines, text } from './lines.js';

const T0 = Date.parse('2025-11-09T12:00:00Z');
const DAY = dayKey(T0);
const NEXT_DAY = T0 + 86_400_000;
const SLUG = FIXTURE_SLUG;
const ACCOUNT = Object.freeze({
  id: 'acct-1',
  firebaseProjectId: 'fugaro-dev',
  authUid: FIXTURE_UID,
  fugaroProject: 'fugaro',
});

/** One line's text, by its label. */
function lineText(list, label) {
  const found = lines(list).filter((line) => line.label === label);
  assert.equal(found.length, 1, `expected exactly one ${label} line`);
  return found[0].text;
}

/** A port that records every path it is asked for, and can deliver values --
 * P3.2's double, in this module's own test file. */
class FakePort {
  requests = [];
  listeners = new Map();
  stopped = [];
  nowMs = T0;

  subscribe(path, { onValue, onError }) {
    this.requests.push(path);
    const handle = () => this.stopped.push(path);
    this.listeners.set(path, { onValue, onError });
    return handle;
  }

  /** Deliver a value to whoever subscribed to a path. */
  put(path, value, nowMs = this.nowMs) {
    const listener = this.listeners.get(path);
    assert.ok(listener !== undefined, `nothing subscribed to ${path}`);
    this.nowMs = nowMs;
    listener.onValue(value, nowMs);
  }

  /** Answer every subscribed path with the current tree, as the Realtime
   * Database does when a listener is attached. */
  flush(valueFor, nowMs = this.nowMs) {
    for (const [path, listener] of [...this.listeners]) {
      this.nowMs = nowMs;
      listener.onValue(valueFor(path), nowMs);
    }
  }
}

/** The list, wired the way the app wires it. */
function wired({ readPresence = null } = {}) {
  const port = new FakePort();
  // The account is opened before the list starts, which is the app's own order
  // (P3.2's reader refuses an account that is not open: nothing would ever stop
  // its listeners) and the reason `start()` is asserted below.
  const registry = new ResourceRegistry();
  registry.open(ACCOUNT.id);
  const list = createJobList({
    port,
    account: ACCOUNT,
    repositories: [SLUG],
    registry,
    readPresence,
    now: () => port.nowMs,
  });
  return { port, list, registry };
}

/** Every node of one recorded day, by path. */
function treeAt(day) {
  const nodes = recordedNodes({ day });
  return (path) => nodes[path] ?? null;
}

test('the list is built from the feed and the mailbox, and the feed\'s words are carried', () => {
  const { port, list } = wired();
  assert.equal(list.start(T0).ok, true);
  port.flush(treeAt(DAY), T0);
  const drawn = list.snapshot({ nowMs: T0 });
  assert.equal(drawn.ok, true);
  assert.equal(drawn.project, 'fugaro');
  assert.equal(drawn.day, dayDate(DAY));
  assert.equal(drawn.dayChanged, false);
  assert.equal(drawn.broken, false);
  assert.equal(drawn.degraded, false);
  assert.equal(drawn.connection, 'live');
  assert.equal(drawn.heartbeat.state, 'unknown', 'nothing has polled the mailbox');
  assert.equal(list.stop().ok, true);
});

test('the wiring asks the granted families for paths, and nothing else', () => {
  const { port, list } = wired();
  list.start(T0);
  assert.ok(port.requests.length > 0);
  // The grant's own gate judges the record: `request()` is the one function in
  // the app that says whether a path is covered, and `FAMILIES` names the
  // families it covers. A path the gate refuses is a read the rules deny, and
  // the wiring is not allowed to have asked for one.
  for (const path of port.requests) {
    assert.equal(request(path).ok, true, `${path} leaves the granted families`);
    assert.ok(
      FAMILIES.some((family) => path === family || path.startsWith(`${family}/`)),
      `${path} is in no granted family`,
    );
  }
  for (const denied of DENIED_FAMILIES) {
    assert.equal(port.requests.some((path) => familyOf(path) === denied || path.startsWith(`${denied}/`)), false, denied);
  }
  list.stop();
});

test('the recomputed figures are the fixture\'s: 12.5 of a 50 cap is 25%', () => {
  const { port, list } = wired();
  list.start(T0);
  port.flush(treeAt(DAY), T0);
  const { row } = list.snapshot({ nowMs: T0 });
  // `recordedNodes` writes counted 12_500_000 micros against a 50 USD global
  // cap: the fraction is the feed's own recompute (`bar()`), carried through.
  assert.equal(row.counted.value, 12.5);
  assert.equal(row.spent.value, 12);
  assert.equal(row.dailyCap.value, 50);
  assert.equal(row.percent.value, 25);
  // No burn window has spanned the warmup, so the rate is **not shown** and
  // carries no number rather than a zero.
  assert.equal(row.burn.shown, false);
  assert.equal(row.burn.usdPerMin.value, null);
  list.stop();
});

test('a changed day is a break, and the new day draws no number', () => {
  const { port, list } = wired();
  list.start(T0);
  port.flush(treeAt(DAY), T0);
  assert.equal(list.snapshot({ nowMs: T0 }).broken, false);

  // The rollover: `refresh` moves the reader to the day the clock is in, and
  // §10.7 says the series broke there -- the new day's counter has not been
  // heard.
  assert.equal(list.refresh(NEXT_DAY).dayChanged, true);
  const broken = list.snapshot({ nowMs: NEXT_DAY });
  assert.equal(broken.ok, true);
  assert.equal(broken.day, dayDate(dayKey(NEXT_DAY)));
  assert.equal(broken.dayChanged, true);
  assert.equal(broken.broken, true);
  assert.equal(broken.row, null);
  // The rollover document carries a zero counter, and none of it is drawn.
  // The check is the **figures**, not the whole screen: the day's own name
  // ("2025-11-10") holds a `0` and is not a spend.
  for (const label of ['Spend (counted)', 'Spend (spent)', 'Daily cap', 'Cap used', 'Burn rate']) {
    assert.equal(lineText(broken, label), GAP_MARK, label);
  }
  assert.equal(text(broken).includes('Spend (counted): 0'), false, 'the rollover zero is never drawn');

  // ... and it measures again once the new day's counter arrives.
  port.flush(treeAt(dayKey(NEXT_DAY)), NEXT_DAY);
  const back = list.snapshot({ nowMs: NEXT_DAY });
  assert.equal(back.broken, false);
  assert.equal(back.row.percent.value, 25);
  list.stop();
});

test('no repository name and no run title reaches the screen', () => {
  const { port, list } = wired();
  list.start(T0);
  port.flush(treeAt(DAY), T0);
  const screen = text(list.snapshot({ nowMs: T0 }));
  // The document the reader assembled carries the repository's block -- the
  // grant covers `config/caps/repos/$slug` and `spend/$day/repos/$slug` -- and
  // the list draws none of it: not the slug it is keyed by, and not the
  // repository's own name.
  assert.equal(screen.includes(SLUG), false, 'the slug is not on the screen');
  assert.equal(screen.includes('example/one'), false, 'the repository name is not on the screen');
  assert.equal(screen.includes('repos'), false);
  list.stop();
});

test('a poll reports the heartbeat, and a client refusal is passed through unchanged', async () => {
  const report = { ok: true, presence: { state: 'live', verified: true, reason: 'fresh', ageMs: 1_000 } };
  const { port, list } = wired({ readPresence: async () => report });
  list.start(T0);
  port.flush(treeAt(DAY), T0);
  const polled = await list.poll();
  assert.deepEqual({ ok: polled.ok, polled: polled.polled }, { ok: true, polled: true });
  assert.equal(list.snapshot({ nowMs: T0 }).heartbeat.state, 'live');

  // A refusal from the mailbox client is the client's, and this module does not
  // re-label it.
  const refused = wired({ readPresence: async () => ({ ok: false, code: 'no-key', field: 'macKey' }) });
  refused.list.start(T0);
  const answer = await refused.list.poll();
  assert.deepEqual({ ...answer }, { ok: false, code: 'no-key', field: 'macKey' });
  assert.equal(refused.list.snapshot({ nowMs: T0 }).heartbeat.state, 'unknown');
  list.stop();
  refused.list.stop();
});

test('a reading with a negative age is stored and drawn, not refused', async () => {
  // The phone's clock ahead of the orchestrator's is D117's `clock-differs`, and
  // P1.4 reports it with a **negative** age (`ageMs = now - at`, and that branch
  // is the only one that answers `clock-differs`). A refusal here would leave
  // the heartbeat at `unknown` and, through `jobList`, refuse the whole list --
  // spend, caps and the kill switch -- for the one phone whose clock is skewed,
  // which is the case the state exists for. `heartbeat.test.js` feeds this rule
  // a reading P1.4 actually produced; this is the same reading through the
  // wiring the app uses.
  const presence = { state: 'clock-differs', verified: true, reason: 'future-skew', ageMs: -600_000, agents: null };
  const { port, list } = wired({ readPresence: async () => ({ ok: true, presence }) });
  list.start(T0);
  port.flush(treeAt(DAY), T0);
  const polled = await list.poll();
  assert.deepEqual({ ok: polled.ok, polled: polled.polled }, { ok: true, polled: true });

  const drawn = list.snapshot({ nowMs: T0 });
  assert.equal(drawn.ok, true, 'a skewed clock is not a refusal of the list');
  assert.equal(drawn.heartbeat.ok, undefined, 'a skew is a reading, not a refusal');
  assert.equal(drawn.heartbeat.state, 'clock-differs');
  assert.equal(drawn.heartbeat.ageMs, -600_000, 'the age P1.4 measured, sign and all');
  assert.equal(lineText(drawn, 'Heartbeat'), 'clock-differs');
  assert.equal(text(drawn).includes('Spend (counted)'), true, 'the figures are on the screen');
  list.stop();
});

test('a presence function that answers something else is refused', async () => {
  const bad = wired({ readPresence: async () => 'presence' });
  bad.list.start(T0);
  assert.equal((await bad.list.poll()).code, LIST_CODES.BAD_PRESENCE);
  const wrongState = wired({ readPresence: async () => ({ ok: true, presence: { state: 'dead' } }) });
  wrongState.list.start(T0);
  assert.equal((await wrongState.list.poll()).code, LIST_CODES.BAD_PRESENCE);
  bad.list.stop();
  wrongState.list.stop();
});

test('a snapshot that is not the feed\'s is refused, and so is a liveness word nobody defined', () => {
  for (const notASnapshot of [null, undefined, 'snapshot', 42, []]) {
    const refused = jobList({ snapshot: notASnapshot });
    assert.equal(refused.ok, false, JSON.stringify(notASnapshot));
    assert.equal(refused.code, LIST_CODES.BAD_SNAPSHOT);
  }
  const document = validateDocument({
    project: 'fugaro',
    day: '2026-10-19',
    now: '2026-10-19T12:00:00.000Z',
    connection: { state: 'live', reason: null, age_seconds: 0 },
    total: { counted_usd: 1, burn: { known: false, usd_per_min: null, fast: false }, kill: null },
  }).document;
  const unknownWord = jobList({ snapshot: { liveness: 'dead', document, day: document.day, broken: false } });
  assert.equal(unknownWord.ok, false);
  assert.equal(unknownWord.code, LIST_CODES.BAD_LIVENESS);
});

test('the list refuses a presence reading it cannot render', () => {
  const document = validateDocument({
    project: 'fugaro',
    day: '2026-10-19',
    now: '2026-10-19T12:00:00.000Z',
    connection: { state: 'live', reason: null, age_seconds: 0 },
    total: { counted_usd: 1, burn: { known: false, usd_per_min: null, fast: false }, kill: null },
  }).document;
  const snapshot = { liveness: 'live', document, day: document.day, dayChanged: false, broken: false, degraded: false, note: null };
  assert.equal(jobList({ snapshot, presence: { state: 'offline', ageMs: '3s' } }).code, LIST_CODES.BAD_PRESENCE);
  assert.equal(jobList({ snapshot, presence: null }).ok, true);
});

test('the list is frozen, and holds one reader', () => {
  const { port, list } = wired();
  list.start(T0);
  port.flush(treeAt(DAY), T0);
  const drawn = list.snapshot({ nowMs: T0 });
  assert.ok(Object.isFrozen(drawn));
  assert.ok(Object.isFrozen(drawn.row));
  assert.ok(Object.isFrozen(drawn.heartbeat));
  assert.equal(list.stop().ok, true);
});

test('a list needs a feed reader, and a presence source that is a function', () => {
  assert.throws(() => new JobList({}), TypeError);
  assert.throws(() => new JobList({ reader: {}, readPresence: 'poll' }), TypeError);
  assert.throws(() => new JobList({ reader: { snapshot: () => null }, now: 7 }), TypeError);
});
