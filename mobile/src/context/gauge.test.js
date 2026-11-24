// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The context gauge (mobile identity and pairing plan, task Z1; D2789).
//
// The red step is first: a gauge with no reading says `unavailable`, which is
// the state the app is in -- Z0 (Check 732) found nothing but `/context`
// reports a fullness, and C2's port carries no member for it -- and it is the
// state a screen must be able to draw without a figure. The rest pins what the
// gauge does with a reading it is handed: the reading's own judgement (fail
// closed, one code and one field per way it can be wrong), the two states, and
// the one function a screen prints with, which must never draw the missing
// figure as a nought.
//
// What the tests deliberately do not cover: a source of readings. Z0 found no
// reporter the app can read, so this task wires none, and a test that fed the
// gauge from a session would be testing the module's own fake. The face is
// `{ session, reading }`, and `reading` is what a caller's source produced.

import assert from 'node:assert/strict';
import test from 'node:test';

import { PORT_CODES, SessionPortError } from '../../../shared/mobile-session/port.js';
import { STUB_PROJECT_ID, stubSession } from '../../../shared/mobile-session/stub.js';
import { GAUGE_CODES, GAUGE_REASONS, GAUGE_STATES, contextGauge, gaugeLabel, isGauge } from './index.js';

/** A reading as `/context` reports one: the tokens used and the window. */
function reading(used, total) {
  return { used, total };
}

/** The gauge of a stub session, or a thrown refusal passed on. */
function gaugeOf(over = {}) {
  return contextGauge({ session: stubSession(), ...over });
}

test('a gauge with no reading says unavailable, and says why', () => {
  const { ok, gauge } = gaugeOf();
  assert.equal(ok, true);
  assert.equal(gauge.state, 'unavailable');
  assert.equal(gauge.reason, GAUGE_REASONS.NOT_REPORTED);
  // No number at all: an unmeasured context is not an empty one (§1's rule,
  // `src/feed/availability.js`), and a screen that drew `0%` here would be
  // saying the window is clear when nobody read it.
  assert.equal(gauge.used, null);
  assert.equal(gauge.total, null);
  assert.equal(gauge.percent, null);
  assert.equal(gaugeLabel(gauge), 'unavailable');
});

test('the gauge names the session it measures', () => {
  const { gauge } = gaugeOf();
  assert.equal(gauge.project_id, STUB_PROJECT_ID);
  const other = contextGauge({ session: stubSession({ project_id: 'other-project' }) });
  assert.equal(other.gauge.project_id, 'other-project');
});

test('a reading draws a fullness, with the tokens and the percentage', () => {
  const { ok, gauge } = gaugeOf({ reading: reading(45_000, 200_000) });
  assert.equal(ok, true);
  assert.equal(gauge.state, 'fullness');
  assert.equal(gauge.used, 45_000);
  assert.equal(gauge.total, 200_000);
  assert.equal(gauge.percent, 23);
  assert.equal(gauge.reason, null);
  assert.equal(gaugeLabel(gauge), '23%');
});

test('the percentage is the reading rounded, and an empty window is a measurement', () => {
  assert.equal(gaugeOf({ reading: reading(0, 200_000) }).gauge.percent, 0);
  assert.equal(gaugeOf({ reading: reading(0, 200_000) }).gauge.state, GAUGE_STATES.FULLNESS);
  assert.equal(gaugeOf({ reading: reading(1, 3) }).gauge.percent, 33);
  assert.equal(gaugeOf({ reading: reading(2, 3) }).gauge.percent, 67);
  // One token in a large window rounds to nothing, and it is still a reading
  // and not the `unavailable` state: the figure is `used` and `total`, which a
  // bar draws from, and the percentage is only what the label prints.
  assert.equal(gaugeOf({ reading: reading(1, 200_000) }).gauge.percent, 0);
  assert.equal(gaugeLabel(gaugeOf({ reading: reading(1, 200_000) }).gauge), '0%');
});

test('a full window is 100%, the most it can be', () => {
  const { gauge } = gaugeOf({ reading: reading(200_000, 200_000) });
  assert.equal(gauge.percent, 100);
  assert.equal(gaugeLabel(gauge), '100%');
});

test('a reading the app did not build is refused, one code and one field per way', () => {
  const cases = [
    ['45000/200000', GAUGE_CODES.BAD_READING, 'reading'],
    [[45_000, 200_000], GAUGE_CODES.BAD_READING, 'reading'],
    [{ used: 1, total: 2, note: 'from /context' }, GAUGE_CODES.UNKNOWN_FIELD, 'reading.note'],
    [{ used: 1 }, GAUGE_CODES.MISSING_FIELD, 'reading.total'],
    [{ total: 2 }, GAUGE_CODES.MISSING_FIELD, 'reading.used'],
    [{ used: -1, total: 2 }, GAUGE_CODES.BAD_USED, 'reading.used'],
    [{ used: 1.5, total: 2 }, GAUGE_CODES.BAD_USED, 'reading.used'],
    [{ used: '1', total: 2 }, GAUGE_CODES.BAD_USED, 'reading.used'],
    [{ used: 1, total: 0 }, GAUGE_CODES.BAD_TOTAL, 'reading.total'],
    [{ used: 1, total: 2.5 }, GAUGE_CODES.BAD_TOTAL, 'reading.total'],
    [{ used: 3, total: 2 }, GAUGE_CODES.USED_OVER_TOTAL, 'reading.used'],
  ];
  for (const [bad, code, field] of cases) {
    const answer = gaugeOf({ reading: bad });
    assert.equal(answer.ok, false, `${JSON.stringify(bad)} was accepted`);
    assert.equal(answer.code, code, `${JSON.stringify(bad)} got the wrong code`);
    assert.equal(answer.field, field);
    assert.equal(answer.gauge, undefined);
  }
});

test('a session that is not a port is refused with the port\'s own code', () => {
  // The port's own error and code, passed through unchanged: what is missing
  // from a half-built session (`missing-member`, not `not-a-session`) is the
  // port's judgement and not this module's to re-label.
  for (const notASession of ['stub-project', null, []]) {
    assert.throws(() => contextGauge({ session: notASession }), (error) => {
      assert.ok(error instanceof SessionPortError);
      assert.equal(error.code, PORT_CODES.NOT_A_SESSION);
      return true;
    });
  }
  assert.throws(() => contextGauge({ session: { project_id: STUB_PROJECT_ID } }), (error) => {
    assert.equal(error.code, PORT_CODES.MISSING_MEMBER);
    return true;
  });
  assert.throws(() => contextGauge({}), (error) => {
    assert.equal(error.code, PORT_CODES.NOT_A_SESSION);
    return true;
  });
});

test('a gauge is frozen, and its key set is the whole shape', () => {
  const { gauge } = gaugeOf({ reading: reading(1, 2) });
  assert.ok(Object.isFrozen(gauge));
  assert.deepEqual(Object.keys(gauge).sort(), ['percent', 'project_id', 'reason', 'state', 'total', 'used']);
  assert.ok(isGauge(gauge));
  assert.ok(isGauge(gaugeOf().gauge));
});

test('gaugeLabel refuses what this module did not build', () => {
  const { gauge } = gaugeOf({ reading: reading(1, 2) });
  const cases = [
    {},
    null,
    '50%',
    { ...gauge, percent: 99 },
    { ...gauge, used: 3 },
    { ...gauge, reason: GAUGE_REASONS.NOT_REPORTED },
    { ...gauge, state: GAUGE_STATES.UNAVAILABLE, used: null, total: null, percent: null },
    { ...gaugeOf().gauge, reason: 'somewhere-else' },
    { ...gaugeOf().gauge, percent: 0 },
    { ...gauge, [Symbol('extra')]: true },
  ];
  for (const bad of cases) {
    assert.equal(isGauge(bad), false, `${JSON.stringify(bad)} was accepted as a gauge`);
    assert.throws(() => gaugeLabel(bad), TypeError);
  }
});
