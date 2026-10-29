// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The project's row (mobile plan, Task P3.3; resolved 2026-10-17).
//
// The row is numbers and states, and the two rules this task exists to hold are
// here: **every figure comes from a granted family** (the field table, asserted
// against the feed's own `FAMILIES`), and **a missing field is `unavailable`
// and never `0`** (the contract's §10.13 rule 5, carried into the row).
//
// The documents below are built through the feed's own `validateDocument`, so
// what is on the row is what P3.2's reader would put in a document for the same
// nodes -- a hand-made object with a `counted` key would prove nothing about
// the app.

import assert from 'node:assert/strict';
import test from 'node:test';

import { FAMILIES, STATE, isFigure, isUnavailable, validateDocument } from '../feed/index.js';
import { JOB_FAMILIES, JOB_FIELDS, LIST_CODES, ROW_FIELDS } from './fields.js';
import { projectRow } from './row.js';

const NOW = '2026-10-19T12:00:00.000Z';

/**
 * A live document, as P3.2's reader assembles one from the granted nodes.
 *
 * The defaults are one repository's worth of nothing: 12.5 USD counted against
 * a 50 USD project cap (so the fraction is 25%), no notional worth mentioning,
 * no kill switch and no burn window that has spoken yet.
 */
function documentOf(overrides = {}) {
  const { total = {}, ...rest } = overrides;
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
    repos: [],
    ...rest,
  });
  assert.equal(judged.ok, true, JSON.stringify(judged));
  return judged.document;
}

test('the row carries the table\'s fields, and no others', () => {
  const built = projectRow(documentOf());
  assert.equal(built.ok, true);
  assert.deepEqual(Object.keys(built.row), [...ROW_FIELDS]);
  assert.deepEqual(Object.keys(built.row), JOB_FIELDS.map((field) => field.name));
});

test('every figure of the row comes from a granted family', () => {
  // The table is the claim; `FAMILIES` is the read grant's own list (`paths.js`,
  // tension (b1)). A field read from anywhere else has to be added to the table,
  // and this is the test that then fails.
  for (const field of JOB_FIELDS) {
    for (const family of field.families) {
      assert.ok(FAMILIES.includes(family), `${field.name} reads ${family}, which the grant does not name`);
    }
  }
  assert.deepEqual([...JOB_FAMILIES], ['config/caps', 'config/kill', 'spend']);
  // ... and the families the grant does not name are not in the table at all.
  for (const denied of ['agents', 'outcomes', 'runs', 'fugaro']) {
    assert.equal(JOB_FAMILIES.includes(denied), false, denied);
  }
});

test('the row is the document\'s own figures, judged and not remade', () => {
  const document = documentOf();
  const built = projectRow(document);
  assert.equal(built.row.counted, document.total.counted);
  assert.equal(built.row.spent, document.total.spent);
  assert.equal(built.row.notional, document.total.notional);
  assert.equal(built.row.dailyCap, document.total.dailyCap);
  assert.equal(built.row.percent, document.total.percent);
  for (const name of ['counted', 'spent', 'notional', 'dailyCap', 'percent']) {
    assert.ok(isFigure(built.row[name]), name);
  }
});

test('the cap fraction is the fixture\'s, and a cap nobody set is unavailable, never 0%', () => {
  const set = projectRow(documentOf());
  assert.equal(set.row.percent.value, 25);
  assert.equal(set.row.percent.state, STATE.MEASURED);
  assert.equal(set.row.dailyCap.value, 50);

  // `config/caps/global` with no `dailyMicros`: the node's cap is a pointer
  // (`budget/model.go`), so an unset cap is nil -- and `document.js` reads that
  // as `unavailable`. A zero here would be a project allowed to spend nothing.
  const unset = projectRow(documentOf({ total: { daily_cap_usd: null, percent: null } }));
  assert.equal(isUnavailable(unset.row.dailyCap), true);
  assert.equal(isUnavailable(unset.row.percent), true);
  assert.notEqual(unset.row.dailyCap.state, STATE.MEASURED);
  assert.notEqual(unset.row.percent.state, STATE.MEASURED);
});

test('a missing spend counter is unavailable, never a zero', () => {
  const built = projectRow(documentOf({ total: { counted_usd: null, spent_usd: null, notional_usd: null } }));
  for (const name of ['counted', 'spent', 'notional']) {
    assert.equal(isUnavailable(built.row[name]), true, name);
    assert.equal(built.row[name].value, null, name);
  }
});

test('a burn rate the window cannot speak for is not shown, and carries no number', () => {
  const built = projectRow(documentOf());
  assert.equal(built.row.burn.shown, false);
  assert.equal(isUnavailable(built.row.burn.usdPerMin), true);
  assert.equal(built.row.burn.fast, false);
});

test('a burn rate the window measured is shown with the number', () => {
  const built = projectRow(
    documentOf({ total: { burn: { known: true, usd_per_min: 0.25, fast: true } } }),
  );
  assert.equal(built.row.burn.shown, true);
  assert.equal(built.row.burn.usdPerMin.value, 0.25);
  assert.equal(built.row.burn.fast, true);
});

test('a rate marked known over no number is refused, not rendered as not shown', () => {
  // `known` is the availability state of `usd_per_min` (the contract's own
  // sentence), so this document contradicts itself -- and `document.js` already
  // reads that as `unavailable`. The row must not paper over it.
  const document = documentOf({ total: { burn: { known: true, usd_per_min: null, fast: false } } });
  assert.equal(isUnavailable(document.total.burn.usdPerMin), true);
  const built = projectRow(document);
  assert.equal(built.ok, false);
  assert.equal(built.code, LIST_CODES.BAD_ROW);
});

test('a kill switch is on or off, and off is a state and not a missing value', () => {
  const off = projectRow(documentOf());
  assert.deepEqual(off.row.kill, { on: false, by: null, at: null, reason: null });

  const on = projectRow(
    documentOf({ total: { kill: { by: 'dimipaun', at: '2026-10-19T11:00:00Z', reason: 'over budget' } } }),
  );
  assert.equal(on.row.kill.on, true);
  assert.equal(on.row.kill.by, 'dimipaun');
  assert.equal(on.row.kill.at, '2026-10-19T11:00:00Z');
  assert.equal(on.row.kill.reason, 'over budget');
});

test('a figure the feed did not build is refused, not put on the row', () => {
  // Every field of the row is a **figure** -- a value with an availability
  // state, built by `document.js`. The table's names are also the wire's names,
  // so a hand-rebuilt document is a plausible-looking object whose `dailyCap` is
  // a bare `50`: a row built from it would carry a number `lines.js` cannot
  // print without inventing a mark, and `shown` would be reading a field nothing
  // judged. It is a refusal, and a *row* refusal rather than a document one --
  // the document is a document, and it is the field that is wrong.
  for (const name of ['counted', 'spent', 'notional', 'dailyCap', 'percent']) {
    const hand = structuredClone(documentOf());
    hand.total[name] = 50;
    const built = projectRow(hand);
    assert.equal(built.ok, false, name);
    assert.equal(built.code, LIST_CODES.BAD_ROW, name);
    assert.equal(built.field, name, name);
  }
  // ... and a figure that is missing altogether, which is the same refusal.
  const missing = structuredClone(documentOf());
  delete missing.total.percent;
  assert.equal(projectRow(missing).code, LIST_CODES.BAD_ROW);
});

test('a kill switch the feed did not build is refused', () => {
  // A document a caller rebuilt by hand rather than one the feed judged: the
  // shape is right and the kill block is not, and the row is refused rather
  // than rendered from a field nothing checked.
  const hand = structuredClone(documentOf());
  hand.total.kill = 'yes';
  const built = projectRow(hand);
  assert.equal(built.ok, false);
  assert.equal(built.code, LIST_CODES.BAD_ROW);
  assert.equal(built.field, 'kill');
});

test('a row is only built from a live document', () => {
  for (const notADocument of [null, {}, { degraded: true }, 'document', 42, []]) {
    const built = projectRow(notADocument);
    assert.equal(built.ok, false, JSON.stringify(notADocument));
    assert.equal(built.code, LIST_CODES.BAD_DOCUMENT);
  }
  // The degraded shape (§2 (iii)) carries no figures at all, and the list
  // renders it as degraded rather than as a row of zeros.
  const degraded = validateDocument({ project: 'fugaro', runs: [], totals: {}, warnings: [], degraded: true, notice: 'no budget backend' });
  assert.equal(degraded.ok, true);
  assert.equal(projectRow(degraded.document).code, LIST_CODES.BAD_DOCUMENT);

  // The degraded shape the feed builds carries no `total` at all, so the check
  // above would pass for the wrong reason. This is the case the flag itself is
  // for: a document a caller rebuilt with `degraded` set and a total beside it.
  // §10.8 -- a degraded read renders as degraded, never as a spend whatever the
  // leftover numbers say.
  const flagged = structuredClone(documentOf());
  flagged.degraded = true;
  const built = projectRow(flagged);
  assert.equal(built.ok, false);
  assert.equal(built.code, LIST_CODES.BAD_DOCUMENT);
  assert.equal(built.field, 'degraded');
});

test('the row is frozen, and so is every entry of the table', () => {
  const built = projectRow(documentOf());
  assert.ok(Object.isFrozen(built.row));
  assert.ok(Object.isFrozen(JOB_FIELDS));
  assert.ok(Object.isFrozen(built.row.burn));
  assert.ok(Object.isFrozen(built.row.kill));
});
