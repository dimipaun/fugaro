// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The overview's tiles (mobile plan, Task P6.8; D170).
//
// Two of P6.8's bullets are asserted here, and the second is the one that
// matters:
//
//   * "the overview renders one tile per installation with its freshness";
//   * "**no summed total** across installations" (D170).
//
// The second is a property about what is *absent*, so the test states it the
// way a regression would appear: every number anywhere in the answer is either
// a figure a single tile carries, or the instant the snapshot was taken -- and
// the sum of two installations' spends is not among them. The tile's key set is
// asserted exactly, for the same reason: a `total` field added in a later task
// would fail this file rather than ship.
//
// The figures are `availability.js`'s own objects, so the printing rules
// (`0` only for a measurement, `—` for a gap) are `shown()`'s and are not
// restated here; what is asserted is which state a tile carries and when.
//
// Nothing here imports the feed's fixtures: a snapshot is built from
// `feed/view.js`'s real shape, and every value is synthetic.

import assert from 'node:assert/strict';
import test from 'node:test';

import { STATE, shown } from '../feed/index.js';
import { TILE_CODES, TILE_FIELDS, TILE_FIGURES, hasFigure, overviewTiles, tileOf } from './tiles.js';

/** A figure of one state, with the value that state allows. */
function fig(state, value = null) {
  return { state, value };
}

/** `feed/view.js`'s snapshot, with this file's synthetic values. */
function snapshot(over = {}) {
  const total = {
    counted: fig(STATE.MEASURED, 12.5),
    spent: fig(STATE.MEASURED, 12.5),
    notional: fig(STATE.NOTIONAL, 12.5),
    dailyCap: fig(STATE.MEASURED, 50),
    percent: fig(STATE.MEASURED, 25),
    runs: fig(STATE.MEASURED, 3),
    burn: { known: true, usdPerMin: fig(STATE.ESTIMATED, 0.4), fast: false },
  };
  return {
    liveness: 'live',
    document: { day: '2026-11-11', total },
    day: '2026-11-11',
    dayChanged: false,
    broken: false,
    drawable: { total, repos: [], figures: [] },
    degraded: false,
    silenceMs: 1_000,
    ageMs: 1_000,
    note: null,
    ingests: 4,
    ...over,
  };
}

/** An account tuple, as `accounts/store.js` lists one. */
function account(over = {}) {
  return { id: 'laptop', firebaseProjectId: 'fp-1234', authUid: 'm~' + 'M'.repeat(21) + 'A', fugaroProject: 'aurora', ...over };
}

test('a tile carries the installation, its freshness and the day’s figures', () => {
  const tile = tileOf({ account: account(), snapshot: snapshot() }).tile;
  assert.deepEqual(Object.keys(tile).sort(), [...TILE_FIELDS].sort());
  assert.equal(tile.id, 'laptop');
  assert.equal(tile.project, 'aurora');
  assert.equal(tile.gcpProject, 'fp-1234');
  assert.equal(tile.liveness, 'live');
  assert.equal(tile.day, '2026-11-11');
  assert.equal(tile.ageMs, 1_000);
  assert.equal(tile.silenceMs, 1_000);
  assert.equal(tile.broken, false);
  assert.equal(tile.degraded, false);
  assert.equal(tile.note, null);
  // The figures are the feed's own, printed by the feed's own rules: a
  // measurement is its number and nothing else.
  assert.deepEqual(tile.spend, fig(STATE.MEASURED, 12.5));
  assert.deepEqual(tile.dailyCap, fig(STATE.MEASURED, 50));
  assert.deepEqual(tile.runs, fig(STATE.MEASURED, 3));
  assert.equal(shown(tile.spend), '12.5');
  assert.equal(hasFigure(tile, 'spend'), true);
});

test('the tile is frozen, and the account’s own fields are not carried whole', () => {
  const tile = tileOf({ account: account({ idToken: 'test-device-token-not-a-credential' }), snapshot: snapshot() }).tile;
  assert.equal(Object.isFrozen(tile), true);
  // The tuple's four fields are the account's; anything else on the record --
  // a field a later task adds, a value a test passes by accident -- is not on
  // the tile, which is what keeps a credential out of a screen by construction.
  assert.deepEqual(Object.keys(tile).sort(), [...TILE_FIELDS].sort());
  assert.equal(JSON.stringify(tile).includes('test-device-token-not-a-credential'), false);
  assert.equal(JSON.stringify(tile).includes('authUid'), false);
});

test('a day break and a degraded document carry no figure, and say why on the tile', () => {
  // §10.7: the day has turned and its counter has not arrived. `view.js` makes
  // `drawable` null so nothing can draw the zero -- "rather than as a figure,
  // because there is no number to carry and a figure is a number's shape" --
  // so the tile has no figure to carry either. What it carries instead is the
  // view's own reason (`broken`, `degraded`, `liveness`), which is what the
  // screen prints the contract's mark from. A tile that minted a gap figure
  // here would be a second producer of a state the feed deliberately has one
  // of: `feed/index.js` puts `GAP_MARK` on its door and keeps `gap()` off it.
  const broken = tileOf({ account: account(), snapshot: snapshot({ broken: true, dayChanged: true, drawable: null }) }).tile;
  assert.equal(broken.dayChanged, true);
  assert.equal(broken.broken, true);
  assert.equal(broken.liveness, 'live');
  assert.equal(broken.spend.state, STATE.UNAVAILABLE);
  assert.equal(shown(broken.spend), 'n/a');
  assert.equal(hasFigure(broken, 'spend'), false);

  const degraded = tileOf({ account: account(), snapshot: snapshot({ degraded: true, drawable: null, day: null }) }).tile;
  assert.equal(degraded.degraded, true);
  assert.equal(degraded.spend.state, STATE.UNAVAILABLE);
  assert.equal(degraded.day, null);

  // A feed that has delivered nothing has no series to break: "not known" is
  // `n/a`, and `liveness` says why. A zero here would be a measurement nobody
  // made.
  const empty = tileOf({
    account: account(),
    snapshot: snapshot({
      liveness: 'no-document',
      document: null,
      day: null,
      broken: true,
      drawable: null,
      ageMs: null,
      silenceMs: null,
    }),
  }).tile;
  assert.equal(empty.spend.state, STATE.UNAVAILABLE);
  assert.equal(shown(empty.spend), 'n/a');
  assert.equal(empty.ageMs, null);
  assert.equal(empty.day, null);
});

test('a total that omits a field says not-known, never zero', () => {
  const thin = { total: { spent: fig(STATE.MEASURED, 4) } };
  const tile = tileOf({ account: account(), snapshot: snapshot({ drawable: thin }) }).tile;
  assert.equal(tile.spend.value, 4);
  assert.equal(tile.dailyCap.state, STATE.UNAVAILABLE);
  assert.equal(tile.percent.state, STATE.UNAVAILABLE);
  assert.equal(tile.runs.state, STATE.UNAVAILABLE);
  // A figure somebody else built is not trusted: a `state` this module does not
  // know is not-known rather than a number.
  const bogus = tileOf({ account: account(), snapshot: snapshot({ drawable: { total: { spent: { state: 'roughly', value: 3 } } } }) }).tile;
  assert.equal(bogus.spend.state, STATE.UNAVAILABLE);
  // And a real zero survives: it is a measurement, and it is the one number the
  // contract forbids inventing.
  const zero = tileOf({ account: account(), snapshot: snapshot({ drawable: { total: { spent: fig(STATE.MEASURED, 0) } } }) }).tile;
  assert.equal(zero.spend.value, 0);
  assert.equal(shown(zero.spend), '0');
});

test('the overview is one tile per installation, in the caller’s order, with no summed total', () => {
  const installations = [
    { account: account({ id: 'laptop' }), snapshot: snapshot({ drawable: { total: { spent: fig(STATE.MEASURED, 10) } } }) },
    { account: account({ id: 'desk', fugaroProject: 'aurora' }), snapshot: snapshot({ drawable: { total: { spent: fig(STATE.MEASURED, 32.5) } } }) },
  ];
  const answer = overviewTiles({ installations, nowMs: 1_700_000_000_000 });
  assert.equal(answer.ok, true);
  assert.equal(answer.tiles.length, 2);
  assert.deepEqual(answer.tiles.map((tile) => tile.id), ['laptop', 'desk']);
  assert.equal(answer.at, 1_700_000_000_000);
  assert.equal(Object.isFrozen(answer.tiles), true);
  assert.equal(shown(answer.tiles[0].spend), '10');
  assert.equal(shown(answer.tiles[1].spend), '32.5');

  // **D170's negative half.** The whole answer carries the tiles' own figures
  // and one instant, and nothing else: 42.5 is the sum of the two spends and it
  // is nowhere in the answer. This is the assertion a "small improvement"
  // (a header total, a combined burn) would fail.
  const answerStrings = JSON.stringify(answer, (key, value) => (key === 'state' ? undefined : value));
  assert.equal(answerStrings.includes('42.5'), false);
  const numbers = [];
  const collect = (value) => {
    if (typeof value === 'number') numbers.push(value);
    else if (value !== null && typeof value === 'object') for (const one of Object.values(value)) collect(one);
  };
  collect(answer);
  assert.deepEqual(numbers.filter((one) => one === 42.5), []);
  // ... and the two tiles are the only things at the top: a field this file does
  // not name is a field somebody added without deciding to.
  assert.deepEqual(Object.keys(answer).sort(), ['at', 'ok', 'tiles']);
  for (const field of TILE_FIGURES) assert.equal(typeof answer[field], 'undefined');
});

test('the overview refuses a list it cannot draw rather than drawing part of one', () => {
  assert.deepEqual(overviewTiles(), { ok: false, code: TILE_CODES.BAD_LIST, field: 'installations' });
  assert.deepEqual(overviewTiles({ installations: 'two' }), { ok: false, code: TILE_CODES.BAD_LIST, field: 'installations' });
  assert.deepEqual(overviewTiles({ installations: [null] }), { ok: false, code: TILE_CODES.BAD_ACCOUNT, field: 'installations' });
  const noAccount = overviewTiles({ installations: [{ snapshot: snapshot() }] });
  assert.equal(noAccount.ok, false);
  assert.equal(noAccount.code, TILE_CODES.BAD_ACCOUNT);
  const noSnapshot = overviewTiles({ installations: [{ account: account() }] });
  assert.equal(noSnapshot.ok, false);
  assert.equal(noSnapshot.code, TILE_CODES.BAD_SNAPSHOT);
  // One bad row refuses the whole answer: a list that drew the good rows and
  // dropped the bad one would show an installation count that is not the
  // owner's, which is D114's list being quietly edited.
  const half = overviewTiles({ installations: [{ account: account(), snapshot: snapshot() }, { account: account() }] });
  assert.equal(half.ok, false);
  assert.equal(half.tiles, undefined);
  // An empty list is a real answer: an owner with no installation yet.
  assert.deepEqual(overviewTiles({ installations: [] }).tiles, []);
});
