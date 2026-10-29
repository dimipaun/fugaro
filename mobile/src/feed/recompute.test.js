// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The two recomputed figures (mobile plan, Task P3.2, the 2026-10-17
// resolution: the burn rate and the cap fractions are recomputed on the phone).
//
// This is the half of the plan's named rule that is arithmetic: "a missing
// field becomes `unavailable` and never `0`" bites hardest here, because a
// recomputed figure is the one a reader is most likely to produce as a zero by
// accident. So the cases below are the pairs of "unknown" and "measured" for
// both figures, at the exact boundaries `internal/watch` uses:
//
//   * the cap fraction is `bar()`'s, and a cap of **zero** is a fraction of
//     100 (it denies everything), while **no cap** is no fraction at all.
//   * the rate needs a **60-second** span and a window that reaches **5
//     minutes** back, and a counter that went backwards has no slope.
//   * `fast` is `perMin*60 > dailyCap/8`, strictly greater, and no cap means
//     no threshold.

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  BURN_WARMUP_MS,
  BURN_WINDOW_MS,
  BurnSampler,
  DEFAULT_BURN_HOURS,
  fast,
  percent,
  recompute,
} from './recompute.js';

test('the constants are the CLI\'s own', () => {
  // `internal/watch/state.go` and `build.go`, and §10.9's table. If the phone
  // ever disagreed with the console about these, the two would report different
  // rates for the same tree and neither would be obviously wrong.
  assert.equal(BURN_WINDOW_MS, 5 * 60_000);
  assert.equal(BURN_WARMUP_MS, 60_000);
  assert.equal(DEFAULT_BURN_HOURS, 8);
});

test('the cap fraction is used over cap, and a cap of zero is full', () => {
  assert.equal(percent(12.5, 50), 25);
  assert.equal(percent(0, 50), 0, 'a measured zero of spend is 0%');
  assert.equal(percent(10, 10), 100);
  assert.equal(percent(15, 10), 150, 'the day can be over its cap, and the bar is drawn past full');
  assert.equal(percent(5, 0), 100, 'a cap of zero denies everything: `bar()` reports 100');
  // No cap is no fraction -- `unavailable`, never 0%.
  assert.equal(percent(5, null), null);
  assert.notEqual(percent(5, null), 0);
  assert.equal(percent(null, 50), null, 'an unread counter is not 0% either');
  assert.notEqual(percent(null, 50), 0);
  for (const bad of [NaN, Infinity, -1, '5', {}, undefined]) {
    assert.equal(percent(bad, 50), null, String(bad));
    assert.equal(percent(5, bad), null, String(bad));
  }
});

test('fast is the day cap spread over eight hours, and no cap is never fast', () => {
  // 50 USD/day over 8 h is 0.104166.../min, and the comparison is strict.
  const threshold = 50 / DEFAULT_BURN_HOURS / 60;
  assert.equal(fast(threshold * 1.01, 50), true);
  assert.equal(fast(threshold, 50), false, 'exactly at the threshold is not fast (`>` in burnOf)');
  assert.equal(fast(threshold * 0.99, 50), false);
  assert.equal(fast(0, 50), false, 'a measured rate of zero is not fast');
  // No cap, no threshold, no fast -- and never "fast because the cap was 0".
  assert.equal(fast(1, null), false);
  assert.equal(fast(1, undefined), false);
  assert.equal(fast(null, 50), false);
  assert.equal(fast(-1, 50), false);
  // A cap of zero is a cap: the threshold is 0, so any rate at all is fast.
  assert.equal(fast(0.0001, 0), true);
  assert.equal(fast(0, 0), false);
});

test('a rate needs a warmup span, and an empty window is unknown not zero', () => {
  const sampler = new BurnSampler();
  const t0 = 1_700_000_000_000;
  // No samples at all: `null`, and emphatically not 0.
  assert.equal(sampler.rate('', t0), null);
  assert.notEqual(sampler.rate('', t0), 0);

  sampler.sample('', t0, 1);
  // One sample is not a slope until the span has grown: the window's first and
  // last are the same reading, so there is nothing between them to divide.
  assert.equal(sampler.rate('', t0), null, 'the span is zero at the sample\'s own instant');
  assert.equal(sampler.rate('', t0 + 1_000), null);
  // Once the warmup has passed it reports the slope it has, which is zero: the
  // counter has not moved since the only reading in the window. That is a
  // measurement of no change, and it is `State.Burn`'s own answer.
  assert.equal(sampler.rate('', t0 + BURN_WARMUP_MS), 0);

  // Two samples 59.999 s apart are still inside the warmup.
  const pair = new BurnSampler();
  pair.sample('', t0, 1);
  pair.sample('', t0 + BURN_WARMUP_MS - 1, 1.5);
  assert.equal(pair.rate('', t0 + BURN_WARMUP_MS - 1), null, 'a span under the warmup reports nothing');

  // ... and at exactly 60 s the slope is real: 0.5 USD in one minute.
  const warm = new BurnSampler();
  warm.sample('', t0, 1);
  warm.sample('', t0 + BURN_WARMUP_MS, 1.5);
  assert.equal(warm.rate('', t0 + BURN_WARMUP_MS), 0.5);
});

test('the slope is per minute, measured from the window\'s first sample', () => {
  const sampler = new BurnSampler();
  const t0 = 1_700_000_000_000;
  sampler.sample('', t0, 0);
  sampler.sample('', t0 + 120_000, 0.04);
  // 0.04 USD over 120 s is 0.02 USD/min.
  assert.equal(sampler.rate('', t0 + 120_000), 0.02);
  // The span is `now - first.at`, not `last.at - first.at`: 0.04 over 240 s.
  assert.equal(sampler.rate('', t0 + 240_000), 0.01);
});

test('a counter that went backwards clears the window, as State.record does', () => {
  const sampler = new BurnSampler();
  const t0 = 1_700_000_000_000;
  sampler.sample('', t0, 5);
  sampler.sample('', t0 + 120_000, 4);
  // A rollover, a reset, a counter that was rewritten: `State.record` calls it
  // "a counter went back: a reset, not negative spend" and starts the window
  // again **from this reading**, so the series that ended contributes nothing
  // to the next slope -- and the rate is unknown until the new window spans the
  // warmup.
  assert.equal(sampler.rate('', t0 + 120_000), null);
  assert.notEqual(sampler.rate('', t0 + 120_000), 0);
  assert.equal(sampler.size(''), 1, 'the reading it went back from is gone, not kept beside it');
  // The new series has a slope of its own, measured from the reading the
  // counter went back to: 0.5 USD over the minute since.
  sampler.sample('', t0 + 180_000, 4.5);
  assert.equal(sampler.rate('', t0 + 180_000), 0.5);
  assert.equal(sampler.size(''), 2);

  // The case this exists for: the day rollover. The new day's counter starts
  // near zero, and the old day's readings must not make its slope negative --
  // nor must they be what it is measured from.
  const rollover = new BurnSampler();
  rollover.sample('', t0, 50);
  rollover.sample('', t0 + 60_000, 0.1);
  assert.equal(rollover.rate('', t0 + 60_000), null);
  rollover.sample('', t0 + 120_000, 0.2);
  assert.ok(Math.abs(rollover.rate('', t0 + 120_000) - 0.1) < 1e-9);
});

test('the window reaches five minutes back and no further', () => {
  const sampler = new BurnSampler();
  const t0 = 1_700_000_000_000;
  sampler.sample('', t0, 0);
  sampler.sample('', t0 + 60_000, 0.06);
  sampler.sample('', t0 + 120_000, 0.12);
  assert.equal(sampler.rate('', t0 + 120_000), 0.06);
  // Six minutes later the first sample is outside the window and is dropped, so
  // the window starts at the 60-second reading: 0.06 USD over the five minutes
  // from there to now. The oldest *kept* sample is always the one just inside
  // the window, which is `State.Burn`'s own arithmetic.
  assert.ok(Math.abs(sampler.rate('', t0 + 360_000) - 0.012) < 1e-9);
  assert.equal(sampler.size(''), 2, 'the window is pruned as it is read');
  // ... and a window whose samples have all aged out reports the slope it has:
  // nothing has moved since the newest reading arrived.
  assert.equal(sampler.rate('', t0 + 10 * 60_000), 0);
  assert.equal(sampler.size(''), 1);
  // The boundary is strict: a sample exactly at `now - window` is kept.
  const boundary = new BurnSampler();
  boundary.sample('', t0, 0);
  boundary.sample('', t0 + BURN_WINDOW_MS, 1);
  // A span of exactly the window: the first sample is kept (the comparison is
  // `at < cut`), so 1 USD over those five minutes is 0.2 USD/min. If it had
  // been dropped, the window would hold one sample and report a slope of 0.
  assert.equal(boundary.rate('', t0 + BURN_WINDOW_MS), 0.2);
  assert.equal(boundary.size(''), 2);
});

test('each counters series has its own window', () => {
  const sampler = new BurnSampler();
  const t0 = 1_700_000_000_000;
  const slug = 'example-one-0123456789abcdef';
  sampler.sample('', t0, 0);
  sampler.sample(slug, t0, 10);
  sampler.sample('', t0 + 120_000, 0.02);
  sampler.sample(slug, t0 + 120_000, 10.04);
  assert.equal(sampler.rate('', t0 + 120_000), 0.01);
  // A slope is a division, so it is compared with a tolerance rather than for
  // exact equality: 0.04/2 is not 0.02 in binary floating point.
  assert.ok(Math.abs(sampler.rate(slug, t0 + 120_000) - 0.02) < 1e-9, 'a repository\'s slope is not the project\'s');
  assert.equal(sampler.rate('another-slug', t0 + 120_000), null, 'an unseen series is unknown');
});

test('a sample that is not a reading is dropped, not stored', () => {
  const sampler = new BurnSampler();
  const t0 = 1_700_000_000_000;
  for (const [at, v] of [
    [t0, null],
    [t0, NaN],
    [t0, -1],
    [t0, '0.5'],
    [null, 1],
    [NaN, 1],
    ['1700000000000', 1],
  ]) {
    assert.equal(sampler.sample('', at, v), false, `${String(at)}/${String(v)}`);
  }
  assert.equal(sampler.size(''), 0, 'a bad sample never enters the window');
  assert.equal(sampler.sample('', t0, 0), true, 'a measured zero is a reading');
  assert.equal(sampler.size(''), 1);
});

test('a reset window reports unknown again, not zero', () => {
  const sampler = new BurnSampler();
  const t0 = 1_700_000_000_000;
  sampler.sample('', t0, 0);
  sampler.sample('', t0 + 120_000, 0.04);
  assert.equal(sampler.rate('', t0 + 120_000), 0.02);
  // `SetDay` empties the windows at the rollover, and a reconnect starts a new
  // one: §10.9's "a restarted window reports `known: false`, not a rate of
  // zero".
  sampler.reset();
  assert.equal(sampler.rate('', t0 + 120_000), null);
  assert.equal(sampler.size(''), 0);
  // One key at a time, for the repository that reconnected alone.
  sampler.sample('a', t0, 0);
  sampler.sample('b', t0, 0);
  sampler.reset('a');
  assert.equal(sampler.rate('a', t0 + 120_000), null);
  assert.equal(sampler.size('b'), 1);
});

test('the recomputed pair is a number or unavailable, never a stray zero', () => {
  assert.deepEqual(recompute({ counted: 12.5, cap: 50, rate: 0.05 }), {
    percent: 25,
    burn: { known: true, usdPerMin: 0.05, fast: false },
  });
  // `fast` is perMin*60 > cap/8: 0.05*60 = 3 USD/h against 50/8 = 6.25, so not
  // fast; 0.5*60 = 30 against the same 6.25, so fast.
  assert.equal(recompute({ counted: 12.5, cap: 50, rate: 0.05 }).burn.fast, false);
  assert.equal(recompute({ counted: 12.5, cap: 50, rate: 0.5 }).burn.fast, true);
  // Every way of not knowing a rate is `known: false` with no number.
  for (const rate of [null, undefined, NaN, Infinity, -1, '0.2']) {
    const out = recompute({ counted: 1, cap: 50, rate });
    assert.equal(out.burn.known, false, String(rate));
    assert.equal(out.burn.usdPerMin, null, String(rate));
    assert.equal(out.burn.fast, false, String(rate));
  }
  // A measured rate of zero is known, and is not fast.
  assert.deepEqual(recompute({ counted: 1, cap: 50, rate: 0 }).burn, { known: true, usdPerMin: 0, fast: false });
  // No cap: no fraction, and the rate is still known -- it is the *fraction*
  // and `fast` that need a cap, not the rate.
  const uncapped = recompute({ counted: 1, cap: null, rate: 0.2 });
  assert.equal(uncapped.percent, null);
  assert.equal(uncapped.burn.known, true);
  assert.equal(uncapped.burn.fast, false);
  assert.notEqual(uncapped.percent, 0);
});
