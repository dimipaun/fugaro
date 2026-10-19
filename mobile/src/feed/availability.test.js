// The five availability states (mobile plan, Task P3.2; json-contract §1).
//
// §1's first rule is the one this file exists for: **every number carries an
// availability state, and a missing value is never an implicit zero.** The
// cases below are the pairs that rule turns on -- a *measured* zero (a real
// reading of nothing) beside an *unavailable* value (no reading at all) -- and
// the ones that make the other three states distinguishable from those two.

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  AVAILABILITY_CODES,
  GAP_MARK,
  STATE,
  check,
  figure,
  gap,
  isFigure,
  measured,
  notional,
  estimated,
  shown,
  unavailable,
} from './availability.js';

test('a measured zero is not an unavailable value, and they render differently', () => {
  const zero = measured(0);
  const missing = unavailable();
  assert.equal(zero.state, STATE.MEASURED);
  assert.equal(zero.value, 0);
  assert.equal(missing.state, STATE.UNAVAILABLE);
  assert.equal(missing.value, null);
  assert.equal(shown(zero), '0');
  assert.equal(shown(missing), 'n/a');
  // The whole point, as one assertion: the two are never the same rendering,
  // whatever the arithmetic would do with them.
  assert.notEqual(shown(zero), shown(missing));
});

test('every state renders as itself, and only measured renders a bare number', () => {
  assert.equal(shown(measured(12.5)), '12.5');
  assert.equal(shown(notional(12.5)), 'NOTIONAL~12.5');
  assert.equal(shown(estimated(12.5)), '~12.5');
  assert.equal(shown(unavailable()), 'n/a');
  assert.equal(shown(gap()), GAP_MARK);
  // Three states, three renderings: a screen that showed a notional figure as a
  // measured one would be reporting a subscription's list price as a bill.
  const rendered = [measured(1), notional(1), estimated(1), unavailable(), gap()].map(shown);
  assert.equal(new Set(rendered).size, rendered.length);
});

test('a gap is its own state, not an unavailable one', () => {
  // The contract keeps them apart: `unavailable` is "the document says the
  // value is not known", `gap` is "the stream stopped, never interpolated
  // across". A series that broke and a value that was never there are
  // different facts about the data, and §10.7 is the rule that needs the
  // second one.
  assert.notEqual(gap().state, unavailable().state);
  assert.equal(gap().value, null);
});

test('a figure is frozen, and a bad one is refused rather than coerced', () => {
  const fig = measured(1);
  assert.ok(Object.isFrozen(fig));
  assert.ok(isFigure(fig));
  // `check` is structural: a value of the right shape, from anywhere, is
  // judged a figure and comes back canonical (frozen, two keys) rather than as
  // the caller's own object. A shape that is wrong is not.
  assert.equal(isFigure({ state: STATE.MEASURED, value: 1 }), true);
  assert.equal(isFigure({ state: STATE.MEASURED, value: 1, extra: 2 }), false);
  assert.equal(isFigure({ state: 'certain', value: 1 }), false);
  assert.equal(isFigure(null), false);
  assert.equal(isFigure(0), false);

  // A measured figure with no number is the shape of the bug this module
  // exists to prevent, so it is a TypeError and not a default.
  for (const bad of [undefined, null, NaN, Infinity, -Infinity, '1', {}]) {
    assert.throws(() => measured(bad), TypeError, `measured(${String(bad)})`);
  }
  // A negative amount is not a measurement of a spend.
  assert.throws(() => measured(-1), TypeError);
  assert.throws(() => figure(1, 'whatever'), TypeError);
  assert.throws(() => figure(1, STATE.UNAVAILABLE), TypeError, 'an unavailable figure carrying a value is a lie');
  assert.throws(() => figure(1, STATE.GAP), TypeError);
  assert.throws(() => figure(null, STATE.MEASURED), TypeError);
});

test('the codes are the refusal vocabulary a caller matches on', () => {
  assert.deepEqual(Object.keys(AVAILABILITY_CODES).sort(), [
    'BAD_VALUE',
    'NEGATIVE',
    'NOT_AN_OBJECT',
    'NOT_A_NUMBER',
    'UNKNOWN_FIELD',
    'UNKNOWN_STATE',
  ]);
  // Each code is reachable, and each one names the reason it is:
  assert.equal(check(null).code, AVAILABILITY_CODES.NOT_AN_OBJECT);
  assert.equal(check({ state: STATE.MEASURED }).code, AVAILABILITY_CODES.UNKNOWN_FIELD);
  assert.equal(check({ state: 'certain', value: 1 }).code, AVAILABILITY_CODES.UNKNOWN_STATE);
  assert.equal(check({ state: STATE.MEASURED, value: '1' }).code, AVAILABILITY_CODES.NOT_A_NUMBER);
  assert.equal(check({ state: STATE.MEASURED, value: -1 }).code, AVAILABILITY_CODES.NEGATIVE);
  assert.equal(check({ state: STATE.UNAVAILABLE, value: 0 }).code, AVAILABILITY_CODES.BAD_VALUE);
  // ... and a good one comes back as a figure this module built, not the
  // caller's own object.
  const judged = check(measured(3));
  assert.equal(judged.ok, true);
  assert.ok(isFigure(judged.figure));
  assert.equal(shown(judged.figure), '3');
  // **The state comes back as it went in.** `measured` is a claim about who
  // read the number, not a default: a notional figure returned as `measured`
  // would promote a subscription's list price to a bill for every caller that
  // reads `.figure`, and `shown` would print it as a bare number.
  for (const fig of [notional(12.5), estimated(2), measured(0), gap(), unavailable()]) {
    const back = check(fig);
    assert.equal(back.ok, true, JSON.stringify(fig));
    assert.equal(back.figure.state, fig.state, JSON.stringify(fig));
    assert.equal(shown(back.figure), shown(fig), JSON.stringify(fig));
  }
  assert.equal(shown(check(notional(12.5)).figure), 'NOTIONAL~12.5');
  assert.equal(check({ state: STATE.NOTIONAL, value: -1 }).code, AVAILABILITY_CODES.NEGATIVE, 'the state is not a way past the value checks');
  assert.equal(check({ state: STATE.ESTIMATED, value: '2' }).code, AVAILABILITY_CODES.NOT_A_NUMBER);
});
