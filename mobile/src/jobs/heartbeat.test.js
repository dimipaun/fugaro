// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The heartbeat line (mobile plan, Task P3.3; resolved 2026-10-17).
//
// This module decides nothing about presence -- P1.4's rule and P3.6's MAC
// check own that -- so what is tested here is exactly two things: the reading is
// carried and not recomputed, and **nothing has been read** is `unknown` rather
// than `offline`. The last one is the reason the file exists: a list that said
// `offline` before the first poll would be reporting a dead other end from the
// absence of a reading.
//
// The readings below are the shape P3.6's `poll()` reports
// (`presence: { state, verified, reason, ageMs, agents }`).

import assert from 'node:assert/strict';
import test from 'node:test';

import { evaluatePresence } from '../../../shared/presence/index.js';
import { HEARTBEAT_STATES, LIST_CODES } from './fields.js';
import { READ_STATES, heartbeatRow } from './heartbeat.js';

/** One reading, as P3.6 reports it. */
function presenceOf(overrides = {}) {
  return { state: 'live', verified: true, reason: 'fresh', ageMs: 3_000, agents: [{ id: 'a' }], ...overrides };
}

test('nothing read yet is unknown, and never offline', () => {
  for (const nothing of [null, undefined]) {
    const row = heartbeatRow(nothing);
    assert.equal(row.ok, undefined, 'no reading is not a refusal');
    assert.equal(row.state, 'unknown');
    assert.equal(row.verified, false);
    assert.equal(row.ageMs, null);
    assert.equal(row.reason, null);
  }
  assert.equal(READ_STATES.includes('unknown'), false, 'unknown is this module\'s, not a reading\'s');
});

test('a reading is carried, not recomputed', () => {
  const row = heartbeatRow(presenceOf({ state: 'stale', reason: 'missed-intervals', ageMs: 190_000 }));
  assert.equal(row.state, 'stale');
  assert.equal(row.reason, 'missed-intervals');
  assert.equal(row.ageMs, 190_000);
  assert.equal(row.verified, true);
});

test('the four words P1.4 can answer with are the four this row carries', () => {
  for (const state of READ_STATES) {
    const row = heartbeatRow(presenceOf({ state }));
    assert.equal(row.state, state);
  }
  assert.deepEqual([...READ_STATES], ['live', 'stale', 'offline', 'clock-differs']);
  assert.deepEqual([...HEARTBEAT_STATES], ['live', 'stale', 'offline', 'clock-differs', 'unknown']);
});

test('an unsigned heartbeat is carried as unverified, and a missing verdict is not verified', () => {
  // P3.6 reports `offline` with `verified: false` for a heartbeat whose tag did
  // not verify; the list carries the verdict rather than re-deriving it.
  const unsigned = heartbeatRow(presenceOf({ state: 'offline', verified: false, reason: 'unsigned', ageMs: null }));
  assert.equal(unsigned.verified, false);
  assert.equal(unsigned.reason, 'unsigned');
  // ... and a reading that says nothing about the tag is not verified either.
  assert.equal(heartbeatRow({ state: 'live' }).verified, false);
});

test('a state this app has never heard of is refused, never printed', () => {
  for (const state of ['dead', 'gone', 'LIVE', '', 42, null, undefined, ['live']]) {
    const row = heartbeatRow(presenceOf({ state }));
    assert.equal(row.ok, false, JSON.stringify(state));
    assert.equal(row.code, LIST_CODES.BAD_PRESENCE);
  }
});

test('the fields a caller could smuggle a claim through are judged', () => {
  for (const bad of [
    { verified: 'yes' },
    { ageMs: '3s' },
    { ageMs: Number.NaN },
    { ageMs: Number.POSITIVE_INFINITY },
    { reason: 7 },
  ]) {
    const row = heartbeatRow(presenceOf(bad));
    assert.equal(row.ok, false, JSON.stringify(bad));
    assert.equal(row.code, LIST_CODES.BAD_PRESENCE);
  }
  for (const notAReading of ['live', 42, [], true]) {
    assert.equal(heartbeatRow(notAReading).code, LIST_CODES.BAD_PRESENCE, JSON.stringify(notAReading));
  }
});

test('a negative age is carried, because P1.4 is where one comes from', () => {
  // `heartbeatRow` is handed P3.6's report, and P3.6 hands P1.4's reading on
  // unchanged -- `ageMs = now - at`. A heartbeat stamped *after* the phone's
  // clock is therefore **negative**: `live` while the skew is inside P1.4's
  // future tolerance, and `clock-differs` beyond it, that branch being `ageMs <
  // -futureToleranceMs` and nothing else (D117's fourth state, which exists for
  // exactly this skew). Refusing the sign made a valid reading a refusal: the
  // wiring dropped it into `unknown`, and `jobList` refused the whole list --
  // spend, caps and the kill switch -- for the one phone whose clock is ahead.
  //
  // The readings below are P1.4's own, run rather than written by hand:
  // `evaluatePresence` is imported and called, so this is the real producer's
  // output and not this test's idea of it.
  const clock = 1_700_000_000_000;
  const heartbeat = (ageMs) => ({ at: clock - ageMs, bootId: ['bo', 'ot'].join('') + '-1' });
  const reading = (ageMs) => evaluatePresence({ heartbeat: heartbeat(ageMs), now: clock, intervalMs: 60_000 });

  const fresh = reading(-60_000);
  assert.equal(fresh.state, 'live', 'within the future tolerance, a future stamp is live');
  assert.equal(fresh.ageMs, -60_000, 'P1.4 carries the sign rather than flooring it');
  const skewed = reading(-600_000);
  assert.equal(skewed.state, 'clock-differs');
  assert.ok(skewed.ageMs < 0, 'a clock difference is negative by construction');

  for (const produced of [fresh, skewed]) {
    const row = heartbeatRow(produced);
    assert.equal(row.ok, undefined, `${produced.state}: a reading P1.4 produced is not a refusal`);
    assert.equal(row.state, produced.state);
    assert.equal(row.ageMs, produced.ageMs, 'carried as P1.4 measured it, and not floored at zero');
  }
});

test('the agent count P3.6 reports is not carried onto the list', () => {
  const row = heartbeatRow(presenceOf({ agents: [{ id: 'a' }, { id: 'b' }] }));
  assert.deepEqual(Object.keys(row), ['state', 'verified', 'reason', 'ageMs']);
  assert.equal('agents' in row, false);
});
