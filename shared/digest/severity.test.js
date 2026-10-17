// The severity ladder's tests (mobile plan, Task P1.3).
//
// The plan's verification line for P1.3 asks for the severity rule "asserted as
// a total order over an exhaustive input table". The exhaustive table over the
// documents lives in `index.test.js`, where the rule is applied; this file is
// the order itself: that the ladder has exactly four rungs, that the order is
// strict and total over them (irreflexive, antisymmetric, transitive), that it
// is the order the plan states -- kill, then a failed finish, then a fast burn,
// then nothing -- and that a severity outside the ladder is refused rather than
// ranked.

import assert from 'node:assert/strict';
import test from 'node:test';

import { DigestError, ERROR } from './errors.js';
import { SEVERITY, SEVERITY_ORDER, moreSevere, mostSevere, severityRank } from './severity.js';

test('the ladder is the four severities, and no fifth', () => {
  assert.deepEqual([...SEVERITY_ORDER], ['kill', 'failed', 'fast_burn', 'info']);
  assert.equal(new Set(SEVERITY_ORDER).size, SEVERITY_ORDER.length);
  assert.deepEqual(
    [...Object.values(SEVERITY)].sort(),
    [...SEVERITY_ORDER].sort(),
  );
});

test('the order is the one the plan states, and it is strict', () => {
  assert.ok(moreSevere(SEVERITY.kill, SEVERITY.failed));
  assert.ok(moreSevere(SEVERITY.kill, SEVERITY.fast_burn));
  assert.ok(moreSevere(SEVERITY.kill, SEVERITY.info));
  assert.ok(moreSevere(SEVERITY.failed, SEVERITY.fast_burn));
  assert.ok(moreSevere(SEVERITY.failed, SEVERITY.info));
  assert.ok(moreSevere(SEVERITY.fast_burn, SEVERITY.info));
  // ... and nothing is more severe than a kill, or less than info.
  assert.equal(mostSevere(SEVERITY_ORDER), SEVERITY.kill);
  assert.equal(mostSevere([...SEVERITY_ORDER].reverse()), SEVERITY.kill);
});

test('the order is total: every pair is comparable, and exactly one way round', () => {
  for (const a of SEVERITY_ORDER) {
    for (const b of SEVERITY_ORDER) {
      const ra = severityRank(a);
      const rb = severityRank(b);
      assert.equal(moreSevere(a, b), ra > rb, `${a} vs ${b}`);
      if (a === b) {
        assert.ok(!moreSevere(a, b), `${a} is not more severe than itself`);
      } else {
        assert.notEqual(moreSevere(a, b), moreSevere(b, a), `${a} vs ${b} is one way round`);
      }
    }
  }
});

test('the order is transitive', () => {
  for (const a of SEVERITY_ORDER) {
    for (const b of SEVERITY_ORDER) {
      for (const c of SEVERITY_ORDER) {
        if (moreSevere(a, b) && moreSevere(b, c)) {
          assert.ok(moreSevere(a, c), `${a} > ${b} > ${c}`);
        }
      }
    }
  }
});

test('a digest with no items is info, never an alarm', () => {
  assert.equal(mostSevere([]), SEVERITY.info);
});

test('a severity outside the ladder is refused, not ranked', () => {
  for (const bad of ['kills', 'KILL', '', 'warning', null, 3]) {
    assert.throws(() => severityRank(bad), (err) => {
      assert.ok(err instanceof DigestError);
      assert.equal(err.code, ERROR.SEVERITY_UNKNOWN);
      return true;
    }, `${JSON.stringify(bad)} should be refused`);
  }
});

test('mostSevere refuses a list carrying a severity outside the ladder', () => {
  assert.throws(() => mostSevere([SEVERITY.kill, 'urgent']), (err) => {
    assert.equal(err.code, ERROR.SEVERITY_UNKNOWN);
    return true;
  });
});
