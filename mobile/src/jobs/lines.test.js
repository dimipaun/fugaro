// What the list says (mobile plan, Task P3.3; resolved 2026-10-17).
//
// The plan's first two cases for this task are about the *screen*: "every
// figure is rendered from a granted family only" and "a missing field is
// `unavailable` and never `0`". The first is `row.test.js`'s table; the second
// is a property of printing, so it is asserted here -- over the strings a
// screen would draw, with the two marks that must never be confused:
//
//   * `n/a` (`shown`'s word for an `unavailable` figure) -- the document did
//     not carry the field;
//   * `—` (the contract's `GAP_MARK`) -- the series broke, and §10.7 forbids
//     drawing the zero the rollover document carries.
//
// A `0` is a **measurement** and appears only when a counter measured zero.

import assert from 'node:assert/strict';
import test from 'node:test';

import { GAP_MARK, validateDocument } from '../feed/index.js';
import { NOTHING } from './fields.js';
import { jobList } from './list.js';
import { NO_REASON, NOT_SHOWN, SERIES, lines, text } from './lines.js';

const NOW = '2026-10-19T12:00:00.000Z';

function documentOf(total = {}) {
  const judged = validateDocument({
    project: 'fugaro',
    day: '2026-10-19',
    now: NOW,
    connection: { state: 'live', reason: null, age_seconds: 0 },
    total: {
      counted_usd: 12.5,
      spent_usd: 12,
      notional_usd: 0.5,
      daily_cap_usd: 50,
      percent: 25,
      burn: { known: false, usd_per_min: null, fast: false },
      kill: null,
      ...total,
    },
  });
  assert.equal(judged.ok, true, JSON.stringify(judged));
  return judged.document;
}

/** A feed snapshot, in the shape `FeedReader.snapshot()` returns. */
function snapshotOf(document, overrides = {}) {
  return {
    liveness: 'live',
    document,
    day: document === null ? null : document.day,
    dayChanged: false,
    broken: false,
    degraded: false,
    note: null,
    ...overrides,
  };
}

/** One line's text, by its label. */
function lineText(list, label) {
  const found = lines(list).filter((line) => line.label === label);
  assert.equal(found.length, 1, `expected exactly one ${label} line`);
  return found[0].text;
}

test('a missing field prints n/a, and never a 0', () => {
  const list = jobList({
    snapshot: snapshotOf(documentOf({ counted_usd: null, spent_usd: null, daily_cap_usd: null, percent: null })),
  });
  assert.equal(list.ok, true);
  assert.equal(lineText(list, 'Spend (counted)'), 'n/a');
  assert.equal(lineText(list, 'Spend (spent)'), 'n/a');
  assert.equal(lineText(list, 'Daily cap'), 'n/a');
  assert.equal(lineText(list, 'Cap used'), 'n/a');
  assert.notEqual(text(list).includes('Spend (counted): 0'), true);
  // The mark is not decorated: an unavailable percentage is not `n/a%`.
  assert.equal(text(list).includes('n/a%'), false);
});

test('a measured zero is a zero, and is not the same screen as a missing field', () => {
  const measuredZero = jobList({
    snapshot: snapshotOf(documentOf({ counted_usd: 0, daily_cap_usd: 0, percent: 100 })),
  });
  assert.equal(lineText(measuredZero, 'Spend (counted)'), '0');
  // A cap of zero is full, not empty (`bar()`): 100%.
  assert.equal(lineText(measuredZero, 'Cap used'), '100%');

  const missing = jobList({ snapshot: snapshotOf(documentOf({ counted_usd: null })) });
  assert.notEqual(lineText(missing, 'Spend (counted)'), lineText(measuredZero, 'Spend (counted)'));
});

test('a broken series draws the gap mark and never the rollover zero', () => {
  // §10.7: the day's counter has not been heard yet, so the document the reader
  // assembled carries the new day's zero -- and the screen must not draw it.
  const list = jobList({
    snapshot: snapshotOf(documentOf({ counted_usd: 0, spent_usd: 0 }), { broken: true, dayChanged: true }),
  });
  assert.equal(list.row, null);
  assert.equal(lineText(list, 'Series'), SERIES.BROKEN);
  for (const label of ['Spend (counted)', 'Spend (spent)', 'Daily cap', 'Cap used', 'Burn rate', 'Kill switch']) {
    assert.equal(lineText(list, label), GAP_MARK, label);
  }
  assert.equal(text(list).includes(': 0'), false, 'no zero is drawn for a broken series');
});

test('a feed that has sent nothing draws no row and no project', () => {
  const list = jobList({ snapshot: snapshotOf(null, { liveness: 'no-document', broken: true }) });
  assert.equal(list.row, null);
  assert.equal(list.project, null);
  assert.equal(lineText(list, 'Project'), NOTHING);
  assert.equal(lineText(list, 'Day'), NOTHING);
  assert.equal(lineText(list, 'Connection'), 'no-document');
});

test('a degraded feed renders as degraded, not as a zero spend', () => {
  const degraded = validateDocument({
    project: 'fugaro',
    runs: [],
    totals: {},
    warnings: [],
    degraded: true,
    notice: 'no budget backend',
  }).document;
  const list = jobList({
    snapshot: snapshotOf(degraded, { liveness: 'degraded', degraded: true, broken: false }),
  });
  assert.equal(list.row, null);
  assert.equal(list.degraded, true);
  assert.equal(lineText(list, 'Spend (counted)'), GAP_MARK);
  assert.equal(text(list).includes('0'), false);
});

test('a cap fraction is printed as a percentage, and a burn rate as a rate', () => {
  const list = jobList({
    snapshot: snapshotOf(documentOf({ burn: { known: true, usd_per_min: 0.25, fast: true } })),
  });
  assert.equal(lineText(list, 'Cap used'), '25%');
  assert.equal(lineText(list, 'Burn rate'), '0.25 (fast)');
});

test('a rate the window cannot speak for says so, and is not a zero', () => {
  const list = jobList({ snapshot: snapshotOf(documentOf()) });
  assert.equal(lineText(list, 'Burn rate'), NOT_SHOWN);
  assert.equal(text(list).includes('Burn rate: 0'), false);
});

test('a kill switch is on or off, and says why when it is on', () => {
  const off = jobList({ snapshot: snapshotOf(documentOf()) });
  assert.equal(lineText(off, 'Kill switch'), 'off');

  const on = jobList({
    snapshot: snapshotOf(documentOf({ kill: { by: 'dimipaun', at: '2026-10-19T11:00:00Z', reason: 'over budget' } })),
  });
  assert.equal(lineText(on, 'Kill switch'), 'on (over budget)');

  const silent = jobList({ snapshot: snapshotOf(documentOf({ kill: { by: null, at: null, reason: null } })) });
  assert.equal(lineText(silent, 'Kill switch'), `on (${NO_REASON})`);
});

test('the heartbeat is unknown until something has read it, and carries its age after', () => {
  const unread = jobList({ snapshot: snapshotOf(documentOf()) });
  assert.equal(lineText(unread, 'Heartbeat'), 'unknown');

  const read = jobList({
    snapshot: snapshotOf(documentOf()),
    presence: { state: 'live', verified: true, reason: 'fresh', ageMs: 3_000 },
  });
  assert.equal(lineText(read, 'Heartbeat'), 'live (3s ago)');

  const unsigned = jobList({
    snapshot: snapshotOf(documentOf()),
    presence: { state: 'offline', verified: false, reason: 'unsigned', ageMs: null },
  });
  assert.equal(lineText(unsigned, 'Heartbeat'), 'offline');
});

test('the feed\'s notice is the only sentence the feed can put on the screen', () => {
  const withNote = jobList({
    snapshot: snapshotOf(documentOf(), { note: 'part of the feed could not be read' }),
  });
  assert.equal(lineText(withNote, 'Note'), 'part of the feed could not be read');
  const without = jobList({ snapshot: snapshotOf(documentOf()) });
  assert.equal(lines(without).some((line) => line.label === 'Note'), false);
});

test('the lines are the table\'s fields, in the table\'s order, and frozen', () => {
  const list = jobList({ snapshot: snapshotOf(documentOf()) });
  const drawn = lines(list);
  assert.deepEqual(
    drawn.map((line) => line.label),
    ['Project', 'Day', 'Series', 'Connection', 'Spend (counted)', 'Spend (spent)', 'Spend (notional)', 'Daily cap', 'Cap used', 'Burn rate', 'Kill switch', 'Heartbeat'],
  );
  assert.ok(Object.isFrozen(drawn));
  assert.ok(drawn.every((line) => Object.isFrozen(line)));
});

test('a field the feed did not build is not printed as a number', () => {
  // `lines` is reachable with a list a caller assembled by hand (it asks only
  // that the list be one `jobList` shaped). A field that is not a figure is
  // **not a number**: `5` here is a bare value with no availability state, and
  // printing it would put a measurement on the screen that nothing measured.
  // The gap mark is what a figure-less field draws.
  const hand = {
    ok: true,
    project: 'fugaro',
    day: '2026-10-19',
    broken: false,
    connection: 'live',
    note: null,
    row: { ...jobList({ snapshot: snapshotOf(documentOf()) }).row, counted: 5 },
    heartbeat: { state: 'unknown', verified: false, reason: null, ageMs: null },
  };
  assert.equal(lineText(hand, 'Spend (counted)'), GAP_MARK);
  assert.equal(text(hand).includes('Spend (counted): 5'), false);
});

test('lines needs a list, not a refusal', () => {
  const refused = jobList({ snapshot: null });
  assert.equal(refused.ok, false);
  assert.throws(() => lines(refused), TypeError);
  assert.throws(() => lines(null), TypeError);
  assert.throws(() => text('a list'), TypeError);
});
