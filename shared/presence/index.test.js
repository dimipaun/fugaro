// The staleness and offline rule's tests (mobile plan, Task P1.4).
//
// Written BEFORE the module it imports, on purpose: that is this task's red
// step. `npm test` from `shared/` discovers this file through P0.1's glob and
// fails with ERR_MODULE_NOT_FOUND until `shared/presence/index.js` exists, so
// the run is red for the right reason and not because no test was found -- the
// one way this package's runner can lie (see `shared/README.md`).
//
// What the tests pin, from the plan's P1.4 and from D117 (answered 2026-10-17):
//
//   * a heartbeat younger than N intervals is `live`;
//   * older than N is `stale`;
//   * far older is `offline`;
//   * a heartbeat whose timestamp is in the FUTURE beyond the bound is
//     `clock-differs`, and NOT `offline` -- D117's "the server's timestamp is
//     the authority, never the app's clock";
//   * a missing heartbeat is `offline`, and is NEVER `live`;
//   * the rule is a pure function of (heartbeat, now, interval): no clock of
//     its own, no randomness, no state, nothing to await.
//
// And, because this package's rule is that ambiguity is refused rather than
// coerced, the refusal half of the table: a wrong type, an unknown field, a
// missing field, a non-integer, a counter-shaped timestamp, an interval below
// D117's floor and two bands that do not nest are each a REFUSAL with its own
// code -- never a state, and never an `offline` guessed from garbage.
//
// No test value here is a secret, and no literal is secret-shaped: the boot ids
// are built at run time from harmless pieces rather than written as anything a
// scanner could mistake for a credential.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import {
  DEFAULT_FUTURE_TOLERANCE_MS,
  DEFAULT_INTERVAL_MS,
  DEFAULT_MISSED_INTERVALS,
  DEFAULT_OFFLINE_INTERVALS,
  MAX_BAND,
  MAX_BOOT_ID_LENGTH,
  MAX_INTERVAL_MS,
  MIN_INTERVAL_MS,
  REASON,
  REFUSAL,
  STATE,
  evaluatePresence,
} from './index.js';

// A fixed `now`, and a fixed heartbeat timestamp, chosen deliberately far from
// the day this test runs: the module must read the `now` it is handed, so a
// rule that quietly called Date.now() would call every one of these
// heartbeats old. See "reads the injected now, never a clock of its own".
const NOW = 1_700_000_000_000;
const INTERVAL = 60_000;

/** A boot id built at run time, so no literal in this file looks like a secret. */
function bootId(...pieces) {
  return ['bo', 'ot'].join('') + '-' + pieces.join('-');
}

/** A heartbeat `ageMs` old at NOW, or in the future when `ageMs` is negative. */
function heartbeatAged(ageMs, boot = bootId('1')) {
  return { at: NOW - ageMs, bootId: boot };
}

test('a heartbeat younger than N intervals is live', () => {
  const result = evaluatePresence({
    heartbeat: heartbeatAged(0),
    now: NOW,
    intervalMs: INTERVAL,
  });

  assert.equal(result.ok, true);
  assert.equal(result.state, STATE.LIVE);
  assert.equal(result.reason, REASON.FRESH);
  assert.equal(result.ageMs, 0);
  assert.equal(result.bootId, bootId('1'));
});

test('a heartbeat one millisecond short of the band is still live', () => {
  const staleAfterMs = INTERVAL * DEFAULT_MISSED_INTERVALS;
  const result = evaluatePresence({
    heartbeat: heartbeatAged(staleAfterMs - 1),
    now: NOW,
    intervalMs: INTERVAL,
  });

  assert.equal(result.state, STATE.LIVE);
  assert.equal(result.staleAfterMs, staleAfterMs);
});

test('a heartbeat exactly at the band is stale, not live: the band is inclusive', () => {
  const staleAfterMs = INTERVAL * DEFAULT_MISSED_INTERVALS;
  const result = evaluatePresence({
    heartbeat: heartbeatAged(staleAfterMs),
    now: NOW,
    intervalMs: INTERVAL,
  });

  assert.equal(result.state, STATE.STALE);
  assert.equal(result.reason, REASON.MISSED_INTERVALS);
  assert.equal(result.ageMs, staleAfterMs);
});

test('a heartbeat older than N intervals is stale', () => {
  const result = evaluatePresence({
    heartbeat: heartbeatAged(INTERVAL * 5),
    now: NOW,
    intervalMs: INTERVAL,
  });

  assert.equal(result.state, STATE.STALE);
});

test('a heartbeat one millisecond short of the offline band is stale, not offline', () => {
  const offlineAfterMs = INTERVAL * DEFAULT_OFFLINE_INTERVALS;
  const result = evaluatePresence({
    heartbeat: heartbeatAged(offlineAfterMs - 1),
    now: NOW,
    intervalMs: INTERVAL,
  });

  assert.equal(result.state, STATE.STALE);
});

test('a heartbeat exactly at the offline band is offline', () => {
  const offlineAfterMs = INTERVAL * DEFAULT_OFFLINE_INTERVALS;
  const result = evaluatePresence({
    heartbeat: heartbeatAged(offlineAfterMs),
    now: NOW,
    intervalMs: INTERVAL,
  });

  assert.equal(result.state, STATE.OFFLINE);
  assert.equal(result.reason, REASON.LONG_SILENCE);
  assert.equal(result.offlineAfterMs, offlineAfterMs);
});

test('a heartbeat far older than the offline band is offline', () => {
  const result = evaluatePresence({
    heartbeat: heartbeatAged(INTERVAL * 1_000),
    now: NOW,
    intervalMs: INTERVAL,
  });

  assert.equal(result.state, STATE.OFFLINE);
});

test('a heartbeat in the future beyond the bound is clock differs, NOT offline', () => {
  const result = evaluatePresence({
    heartbeat: heartbeatAged(-DEFAULT_FUTURE_TOLERANCE_MS - 1),
    now: NOW,
    intervalMs: INTERVAL,
  });

  assert.equal(result.state, STATE.CLOCK_DIFFERS);
  assert.equal(result.reason, REASON.FUTURE_SKEW);
  assert.notEqual(result.state, STATE.OFFLINE);
  assert.notEqual(result.state, STATE.LIVE);
});

test('a heartbeat in the future within the tolerance bound is live, not clock differs', () => {
  const result = evaluatePresence({
    heartbeat: heartbeatAged(-DEFAULT_FUTURE_TOLERANCE_MS),
    now: NOW,
    intervalMs: INTERVAL,
  });

  assert.equal(result.state, STATE.LIVE);
});

test('a future heartbeat far past the bound is never reported live', () => {
  // The naive reading -- "age is negative, so it is fresh" -- would call this
  // live. D117 says the phone's own clock is not the authority, so a skew this
  // large is reported as a clock difference and never as a healthy heartbeat.
  const result = evaluatePresence({
    heartbeat: heartbeatAged(-INTERVAL * 10_000),
    now: NOW,
    intervalMs: INTERVAL,
  });

  assert.equal(result.state, STATE.CLOCK_DIFFERS);
});

test('a future skew is a refusal of "offline" even when it is years away', () => {
  const heartbeat = { at: NOW + 5 * 365 * 24 * 60 * 60 * 1_000, bootId: bootId('9') };
  const result = evaluatePresence({ heartbeat, now: NOW, intervalMs: INTERVAL });

  assert.equal(result.ok, true);
  assert.equal(result.state, STATE.CLOCK_DIFFERS);
});

test('a missing heartbeat is offline and never live', () => {
  for (const heartbeat of [null, undefined]) {
    const result = evaluatePresence({ heartbeat, now: NOW, intervalMs: INTERVAL });

    assert.equal(result.ok, true);
    assert.equal(result.state, STATE.OFFLINE);
    assert.equal(result.reason, REASON.NO_HEARTBEAT);
    assert.notEqual(result.state, STATE.LIVE);
    assert.equal(result.ageMs, null);
    assert.equal(result.bootId, null);
  }
});

test('an absent heartbeat field is offline too, and so is an omitted input', () => {
  assert.equal(
    evaluatePresence({ now: NOW, intervalMs: INTERVAL }).state,
    STATE.OFFLINE,
  );
  const result = evaluatePresence();
  assert.equal(result.ok, false);
  // A missing input still carries no `now`, and an absent now is refused
  // rather than assumed -- the refusal is the point, not the state.
  assert.equal(result.code, REFUSAL.INVALID_NOW);
});

test('the rule is a pure function of heartbeat, now and interval', () => {
  const input = { heartbeat: heartbeatAged(INTERVAL * 4), now: NOW, intervalMs: INTERVAL };
  const first = evaluatePresence(input);
  const second = evaluatePresence(input);

  assert.deepEqual(first, second);
  assert.notEqual(first, second, 'the result is a value, not shared mutable state');

  // Only the interval changed, and the verdict changed with it.
  assert.equal(first.state, STATE.STALE);
  assert.equal(
    evaluatePresence({ ...input, intervalMs: INTERVAL * 10 }).state,
    STATE.LIVE,
  );
});

test('reads the injected now, never a clock of its own', () => {
  // NOW is in 2023. A module that used Date.now() would call this heartbeat two
  // years old and report offline; the injected now makes it fresh.
  const result = evaluatePresence({
    heartbeat: { at: NOW - INTERVAL, bootId: bootId('c') },
    now: NOW,
    intervalMs: INTERVAL,
  });

  assert.equal(result.state, STATE.LIVE);
});

test('a paused interval is a parameter: N and the offline band move the verdicts', () => {
  const heartbeat = heartbeatAged(INTERVAL * 6);

  assert.equal(
    evaluatePresence({
      heartbeat,
      now: NOW,
      intervalMs: INTERVAL,
      missedIntervals: 6,
      offlineIntervals: 12,
    }).state,
    STATE.STALE,
  );
  assert.equal(
    evaluatePresence({
      heartbeat,
      now: NOW,
      intervalMs: INTERVAL,
      missedIntervals: 6,
      offlineIntervals: 7,
    }).state,
    STATE.STALE,
  );
  assert.equal(
    evaluatePresence({
      heartbeat,
      now: NOW,
      intervalMs: INTERVAL,
      missedIntervals: 7,
      offlineIntervals: 12,
    }).state,
    STATE.LIVE,
  );
});

test('the future bound is a parameter, and it defaults to one interval', () => {
  assert.equal(DEFAULT_FUTURE_TOLERANCE_MS, DEFAULT_INTERVAL_MS);

  const heartbeat = heartbeatAged(-INTERVAL * 2);
  assert.equal(
    evaluatePresence({ heartbeat, now: NOW, intervalMs: INTERVAL }).state,
    STATE.CLOCK_DIFFERS,
  );
  assert.equal(
    evaluatePresence({
      heartbeat,
      now: NOW,
      intervalMs: INTERVAL,
      futureToleranceMs: INTERVAL * 2,
    }).state,
    STATE.LIVE,
  );
});

test('an interval below D117\'s floor is refused, never clamped', () => {
  const result = evaluatePresence({
    heartbeat: heartbeatAged(0),
    now: NOW,
    intervalMs: MIN_INTERVAL_MS - 1,
  });

  assert.equal(result.ok, false);
  assert.equal(result.code, REFUSAL.INTERVAL_BELOW_FLOOR);
  assert.equal(result.state, undefined);
});

test('the floor itself is accepted, and it is above zero', () => {
  assert.ok(MIN_INTERVAL_MS > 0);
  assert.equal(
    evaluatePresence({ heartbeat: heartbeatAged(0), now: NOW, intervalMs: MIN_INTERVAL_MS })
      .state,
    STATE.LIVE,
  );
});

test('the default interval is at or above the floor and within the ceiling', () => {
  assert.ok(DEFAULT_INTERVAL_MS >= MIN_INTERVAL_MS);
  assert.ok(DEFAULT_INTERVAL_MS <= MAX_INTERVAL_MS);
  assert.ok(DEFAULT_OFFLINE_INTERVALS > DEFAULT_MISSED_INTERVALS);
});

test('every malformed input is refused with its own code, and never coerced', () => {
  const good = { heartbeat: heartbeatAged(INTERVAL), now: NOW, intervalMs: INTERVAL };

  const cases = [
    // The input itself.
    { input: 'live', code: REFUSAL.INPUT_NOT_AN_OBJECT },
    { input: 42, code: REFUSAL.INPUT_NOT_AN_OBJECT },
    { input: [], code: REFUSAL.INPUT_NOT_AN_OBJECT },
    { input: null, code: REFUSAL.INPUT_NOT_AN_OBJECT },
    { input: { ...good, extra: 1 }, code: REFUSAL.UNKNOWN_FIELD },

    // The heartbeat's shape.
    { input: { ...good, heartbeat: 'fresh' }, code: REFUSAL.INVALID_HEARTBEAT },
    { input: { ...good, heartbeat: 1_700_000_000_000 }, code: REFUSAL.INVALID_HEARTBEAT },
    { input: { ...good, heartbeat: [] }, code: REFUSAL.INVALID_HEARTBEAT },
    { input: { ...good, heartbeat: new Date(NOW) }, code: REFUSAL.INVALID_HEARTBEAT },
    { input: { ...good, heartbeat: {} }, code: REFUSAL.HEARTBEAT_MISSING_FIELD },
    {
      input: { ...good, heartbeat: { at: NOW - INTERVAL } },
      code: REFUSAL.HEARTBEAT_MISSING_FIELD,
    },
    {
      input: { ...good, heartbeat: { bootId: bootId('1') } },
      code: REFUSAL.HEARTBEAT_MISSING_FIELD,
    },
    {
      input: { ...good, heartbeat: { ...heartbeatAged(INTERVAL), sentAt: NOW } },
      code: REFUSAL.HEARTBEAT_UNKNOWN_FIELD,
    },

    // The heartbeat's values: no coercion, no counter-shaped timestamp.
    {
      input: { ...good, heartbeat: { at: String(NOW - INTERVAL), bootId: bootId('1') } },
      code: REFUSAL.INVALID_HEARTBEAT_TIMESTAMP,
    },
    {
      input: { ...good, heartbeat: { at: NOW - INTERVAL + 0.5, bootId: bootId('1') } },
      code: REFUSAL.INVALID_HEARTBEAT_TIMESTAMP,
    },
    {
      input: { ...good, heartbeat: { at: Number.NaN, bootId: bootId('1') } },
      code: REFUSAL.INVALID_HEARTBEAT_TIMESTAMP,
    },
    {
      input: { ...good, heartbeat: { at: Number.POSITIVE_INFINITY, bootId: bootId('1') } },
      code: REFUSAL.INVALID_HEARTBEAT_TIMESTAMP,
    },
    {
      input: { ...good, heartbeat: { at: 0, bootId: bootId('1') } },
      code: REFUSAL.INVALID_HEARTBEAT_TIMESTAMP,
    },
    {
      input: { ...good, heartbeat: { at: -1, bootId: bootId('1') } },
      code: REFUSAL.INVALID_HEARTBEAT_TIMESTAMP,
    },
    {
      input: { ...good, heartbeat: { at: Number.MAX_SAFE_INTEGER + 2, bootId: bootId('1') } },
      code: REFUSAL.INVALID_HEARTBEAT_TIMESTAMP,
    },

    // The boot id.
    {
      input: { ...good, heartbeat: { at: NOW - INTERVAL, bootId: '' } },
      code: REFUSAL.INVALID_BOOT_ID,
    },
    {
      input: { ...good, heartbeat: { at: NOW - INTERVAL, bootId: 7 } },
      code: REFUSAL.INVALID_BOOT_ID,
    },
    {
      input: {
        ...good,
        heartbeat: { at: NOW - INTERVAL, bootId: 'b'.repeat(MAX_BOOT_ID_LENGTH + 1) },
      },
      code: REFUSAL.INVALID_BOOT_ID,
    },
    {
      input: {
        ...good,
        heartbeat: { at: NOW - INTERVAL, bootId: bootId('1') + '\u0000' },
      },
      code: REFUSAL.INVALID_BOOT_ID,
    },

    // Now, and the numbers that shape the bands.
    { input: { ...good, now: String(NOW) }, code: REFUSAL.INVALID_NOW },
    { input: { ...good, now: 0 }, code: REFUSAL.INVALID_NOW },
    { input: { ...good, now: NOW + 0.5 }, code: REFUSAL.INVALID_NOW },
    { input: { ...good, now: undefined }, code: REFUSAL.INVALID_NOW },
    { input: { ...good, intervalMs: String(INTERVAL) }, code: REFUSAL.INVALID_INTERVAL },
    { input: { ...good, intervalMs: 0 }, code: REFUSAL.INVALID_INTERVAL },
    { input: { ...good, intervalMs: INTERVAL + 0.5 }, code: REFUSAL.INVALID_INTERVAL },
    {
      input: { ...good, intervalMs: MAX_INTERVAL_MS + 1 },
      code: REFUSAL.INTERVAL_ABOVE_CEILING,
    },
    { input: { ...good, missedIntervals: '3' }, code: REFUSAL.INVALID_BAND },
    { input: { ...good, missedIntervals: 0 }, code: REFUSAL.INVALID_BAND },
    { input: { ...good, missedIntervals: MAX_BAND + 1 }, code: REFUSAL.INVALID_BAND },
    { input: { ...good, offlineIntervals: 2.5 }, code: REFUSAL.INVALID_BAND },
    { input: { ...good, offlineIntervals: MAX_BAND + 1 }, code: REFUSAL.INVALID_BAND },
    {
      input: { ...good, futureToleranceMs: 'soon' },
      code: REFUSAL.INVALID_FUTURE_TOLERANCE,
    },
    {
      input: { ...good, futureToleranceMs: -1 },
      code: REFUSAL.INVALID_FUTURE_TOLERANCE,
    },
  ];

  for (const { input, code } of cases) {
    const result = evaluatePresence(input);

    assert.equal(result.ok, false, `expected a refusal for ${JSON.stringify(input)}`);
    assert.equal(result.code, code, `wrong code for ${JSON.stringify(input)}`);
    assert.equal(result.state, undefined, 'a refusal carries no state to render');
  }
});

test('two bands that do not nest are refused as ambiguous', () => {
  const result = evaluatePresence({
    heartbeat: heartbeatAged(INTERVAL),
    now: NOW,
    intervalMs: INTERVAL,
    missedIntervals: 6,
    offlineIntervals: 6,
  });

  assert.equal(result.ok, false);
  assert.equal(result.code, REFUSAL.BANDS_NOT_INCREASING);
  assert.equal(result.state, undefined);
});

test('equal bands are refused rather than resolved by an unstated tie-break', () => {
  for (const [missedIntervals, offlineIntervals] of [
    [6, 6],
    [7, 6],
  ]) {
    const result = evaluatePresence({
      heartbeat: heartbeatAged(0),
      now: NOW,
      intervalMs: INTERVAL,
      missedIntervals,
      offlineIntervals,
    });

    assert.equal(result.code, REFUSAL.BANDS_NOT_INCREASING);
  }
});

test('the four states are distinct and are the whole vocabulary', () => {
  assert.deepEqual(
    Object.values(STATE).sort(),
    ['clock-differs', 'live', 'offline', 'stale'],
  );

  const states = new Set([
    evaluatePresence({ heartbeat: heartbeatAged(0), now: NOW, intervalMs: INTERVAL }).state,
    evaluatePresence({
      heartbeat: heartbeatAged(INTERVAL * 5),
      now: NOW,
      intervalMs: INTERVAL,
    }).state,
    evaluatePresence({
      heartbeat: heartbeatAged(INTERVAL * 100),
      now: NOW,
      intervalMs: INTERVAL,
    }).state,
    evaluatePresence({
      heartbeat: heartbeatAged(-INTERVAL * 100),
      now: NOW,
      intervalMs: INTERVAL,
    }).state,
  ]);

  assert.deepEqual([...states].sort(), Object.values(STATE).sort());
});

test('the module README documents every code the module can return', () => {
  // A code that exists but is documented nowhere is a caller's afternoon lost,
  // and a refusal table that quietly covers 13 of 14 codes reads as complete.
  // The README is this module's contract, so the contract is checked against
  // the module rather than trusted.
  const readme = readFileSync(new URL('./README.md', import.meta.url), 'utf8');

  for (const [name, code] of Object.entries(REFUSAL)) {
    assert.ok(
      readme.includes(code),
      `REFUSAL.${name} (${code}) is not documented in shared/presence/README.md`,
    );
  }
  // The four states are checked as their own table cells rather than as
  // substrings, because "the word `clock-differs` appears somewhere in the
  // README" is true of prose that no longer lists it as a state at all.
  const cells = readme
    .split('\n')
    .filter((line) => line.startsWith('|'))
    .flatMap((line) => line.split('|').map((cell) => cell.trim()));

  for (const state of Object.values(STATE)) {
    assert.ok(
      cells.includes(`\`${state}\``),
      `the state ${state} is not listed as a table cell in shared/presence/README.md`,
    );
  }
});

test('every refusal code is distinct and names itself', () => {
  const codes = Object.values(REFUSAL);

  assert.equal(new Set(codes).size, codes.length);
  for (const code of codes) {
    assert.match(code, /^[a-z][a-z0-9-]*$/);
  }
});

test('a value exactly at each documented bound is accepted, one past it is refused', () => {
  const base = { heartbeat: heartbeatAged(0), now: NOW };

  const longestId = evaluatePresence({
    ...base,
    heartbeat: { at: NOW, bootId: 'b'.repeat(MAX_BOOT_ID_LENGTH) },
  });
  assert.equal(longestId.ok, true, 'a boot id of exactly MAX_BOOT_ID_LENGTH is accepted');

  const longestInterval = evaluatePresence({ ...base, intervalMs: MAX_INTERVAL_MS });
  assert.equal(longestInterval.ok, true, 'an interval of exactly MAX_INTERVAL_MS is accepted');
  assert.equal(longestInterval.intervalMs, MAX_INTERVAL_MS);

  const widestMissed = evaluatePresence({
    ...base,
    missedIntervals: MAX_BAND - 1,
    offlineIntervals: MAX_BAND,
  });
  assert.equal(widestMissed.ok, true, 'an offline band of exactly MAX_BAND is accepted');
  const refusedBand = evaluatePresence({
    ...base,
    missedIntervals: MAX_BAND,
    offlineIntervals: MAX_BAND + 1,
  });
  assert.equal(refusedBand.ok, false);
  assert.equal(refusedBand.code, REFUSAL.INVALID_BAND);
  const widestStale = evaluatePresence({
    ...base,
    missedIntervals: MAX_BAND,
    offlineIntervals: MAX_BAND,
  });
  assert.equal(widestStale.code, REFUSAL.BANDS_NOT_INCREASING, 'MAX_BAND passes the band check');
});

test('each input field is read exactly once, so a getter cannot change between reads', () => {
  const flip = (first, second) => {
    let reads = 0;
    return {
      get: () => (++reads === 1 ? first : second),
      reads: () => reads,
    };
  };
  const cases = [
    ['intervalMs', 60_000, 5],
    ['missedIntervals', 3, 0],
    ['offlineIntervals', 10, 0],
    ['futureToleranceMs', 60_000, -1],
  ];
  for (const [field, first, second] of cases) {
    const probe = flip(first, second);
    const input = { heartbeat: heartbeatAged(0), now: NOW };
    Object.defineProperty(input, field, { get: probe.get, enumerable: true });
    const result = evaluatePresence(input);
    assert.equal(result.ok, true, `${field}: the first read is the value used`);
    assert.equal(probe.reads(), 1, `${field} is read exactly once`);
  }
});
