// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// One tile per installation, and never a total across them (mobile plan, Task
// P6.8; D170, and console v2's D32).
//
// **D170 is a rule with a negative half, and the negative half is the point.**
// The overview shows "one tile per installation with its freshness", and it
// shows **no summed total**. A sum across installations would add figures that
// are not in the same unit and not in the same project: an installation's day
// spend is that project's model spend against that project's budget, and two
// installations may be in two currencies, two billing accounts and two days.
// The console reached the same conclusion for the same reason (v2's D32), so
// this is one rule in two clients rather than a taste of the phone's.
//
// **How the rule is held here rather than promised.** This module is a pure
// function of a list, and what it returns is a list of the same length: there
// is no accumulator, no `reduce`, and no field that any two tiles contribute
// to. A test asserts the tile's key set exactly, and that no number anywhere in
// the answer is the sum of two tiles' -- which is the check a later "small
// improvement" would fail.
//
// **What a tile carries.** The installation's own name and project (D114's
// tuple: the label the owner typed and the Fugaro project), and what a reader
// of a phone screen wants from a feed: how alive it is (`liveness`), how old
// the reading is (`ageMs`), how long it has been quiet (`silenceMs`), the day
// the figures belong to, and the day's spend, cap and run count as
// **figures** -- `availability.js`'s objects, printed by `shown()`, so an
// unmeasured day is `n/a` and never a zero.
//
// **Why the figures come from `drawable` and not from `document.total`.**
// `feed/view.js` says why in its own header: `drawable` is `null` on the day
// break and on a degraded document, so that a screen **cannot** draw the zero
// §10.7 forbids by forgetting a check. A tile that read `document.total`
// directly would be that forgotten check, so this module reads the one field
// the view built for drawing and never the document underneath it.
//
// **And when there is nothing drawable, the figure is `gap()`.** `gap()`'s own
// doc comment names exactly two producers: "a changed `day`, a document that is
// not a measurement" -- which are the view's `broken` and its `degraded`, the
// two reasons `drawable` is `null` on a feed that has arrived. So the day break
// prints as `—`, the contract's broken-series mark, and not as `n/a` (a value
// that was never there) and not as `0`. The one case that is *not* a gap is a
// feed that never delivered at all: there is no series to break, so its figures
// are `unavailable()` and its `liveness` is `no-document`.

import { STATE, gap, isFigure, unavailable } from '../feed/availability.js';

/** How this module refuses. */
export const TILE_CODES = Object.freeze({
  BAD_LIST: 'bad-list',
  BAD_ACCOUNT: 'bad-account',
  BAD_SNAPSHOT: 'bad-snapshot',
});

/** The tile's fields, exactly. A field added here is a decision: the screen and
 * the tests both name them. */
export const TILE_FIELDS = Object.freeze([
  'id',
  'project',
  'gcpProject',
  'liveness',
  'day',
  'dayChanged',
  'broken',
  'degraded',
  'ageMs',
  'silenceMs',
  'spend',
  'dailyCap',
  'percent',
  'runs',
  'note',
]);

/** The figures a tile carries, named here so the screen and the test walk one
 * list rather than three literals. */
export const TILE_FIGURES = Object.freeze(['spend', 'dailyCap', 'percent', 'runs']);

function refuse(code, field) {
  return Object.freeze({ ok: false, code, ...(field === undefined ? {} : { field }) });
}

/**
 * The four figures of one snapshot: from `drawable`'s total when the view has
 * something to draw, and otherwise the figure that says why it does not.
 *
 * A total that omits a field -- `runs` and `run_hours` are the total's alone
 * and a document may not carry them -- leaves that figure `unavailable()`, by
 * `document.js`'s own rule: an absent key is "not known", never a zero.
 */
function figuresOf(snapshot) {
  const drawable = snapshot.drawable;
  if (drawable === null || drawable === undefined || typeof drawable !== 'object') {
    const none = snapshot.liveness === 'no-document' ? unavailable() : gap();
    return Object.freeze({ spend: none, dailyCap: none, percent: none, runs: none });
  }
  const total = drawable.total;
  const at = (name) => {
    const found = total === null || typeof total !== 'object' ? undefined : total[name];
    return isFigure(found) ? found : unavailable();
  };
  return Object.freeze({ spend: at('spent'), dailyCap: at('dailyCap'), percent: at('percent'), runs: at('runs') });
}

/**
 * One installation's tile, from its account tuple and its feed view's
 * snapshot: `{ ok: true, tile }`, or a refusal.
 *
 * The answer is wrapped rather than being the tile itself, because the tile's
 * key set is exactly `TILE_FIELDS` and `ok` is not one of them: a tile that
 * carried its own success flag would be a field the screen has to skip, and
 * `TILE_FIELDS` -- the list a test pins -- would be a list of almost all of it.
 *
 * A snapshot that is not one is refused rather than half-drawn: the tile's
 * freshness is the whole of what D170 asks it to carry, and a tile without it
 * is a row that looks alive because it has no state to be otherwise.
 */
export function tileOf({ account, snapshot } = {}) {
  if (account === null || typeof account !== 'object' || typeof account.id !== 'string') {
    return refuse(TILE_CODES.BAD_ACCOUNT, 'id');
  }
  if (snapshot === null || typeof snapshot !== 'object' || typeof snapshot.liveness !== 'string') {
    return refuse(TILE_CODES.BAD_SNAPSHOT, 'liveness');
  }
  const figures = figuresOf(snapshot);
  const tile = Object.freeze({
    id: account.id,
    project: typeof account.fugaroProject === 'string' ? account.fugaroProject : null,
    gcpProject: typeof account.firebaseProjectId === 'string' ? account.firebaseProjectId : null,
    liveness: snapshot.liveness,
    day: typeof snapshot.day === 'string' ? snapshot.day : null,
    dayChanged: snapshot.dayChanged === true,
    broken: snapshot.broken === true,
    degraded: snapshot.degraded === true,
    ageMs: Number.isFinite(snapshot.ageMs) ? snapshot.ageMs : null,
    silenceMs: Number.isFinite(snapshot.silenceMs) ? snapshot.silenceMs : null,
    spend: figures.spend,
    dailyCap: figures.dailyCap,
    percent: figures.percent,
    runs: figures.runs,
    note: typeof snapshot.note === 'string' ? snapshot.note : null,
  });
  return Object.freeze({ ok: true, tile });
}

/**
 * The overview: one tile per installation, in the caller's order.
 *
 * The order is the caller's -- the store's own listing order -- and it is
 * passed through unchanged, so the screen's rows are stable between refreshes
 * and nothing here sorts by a figure: a list ordered by spend would be the
 * summed total's quieter cousin.
 *
 * `installations` is a list of `{ account, snapshot }`, where the snapshot is
 * `feed/view.js`'s `snapshot({ nowMs })` for that installation. `nowMs` is
 * carried on the answer, and is not a port: the tiles are built from snapshots
 * the caller already took at that instant, so this function has no clock of its
 * own to consult and no way to take two readings a moment apart.
 */
export function overviewTiles({ installations, nowMs } = {}) {
  if (!Array.isArray(installations)) return refuse(TILE_CODES.BAD_LIST, 'installations');
  const tiles = [];
  for (const entry of installations) {
    if (entry === null || typeof entry !== 'object') return refuse(TILE_CODES.BAD_ACCOUNT, 'installations');
    const judged = tileOf({ account: entry.account, snapshot: entry.snapshot });
    if (!judged.ok) return judged;
    tiles.push(judged.tile);
  }
  return Object.freeze({ ok: true, tiles: Object.freeze(tiles), at: Number.isFinite(nowMs) ? nowMs : null });
}

/**
 * Whether a tile's named figure is a number the screen may draw, rather than a
 * state that says why it is not.
 *
 * The screen asks this rather than reading `.state` itself, so the two cannot
 * disagree about what "measured" means -- and so that a later sixth state is
 * one edit here rather than a hunt through a render function.
 */
export function hasFigure(tile, field) {
  if (tile === null || typeof tile !== 'object') return false;
  const fig = tile[field];
  return isFigure(fig) && fig.state !== STATE.UNAVAILABLE && fig.state !== STATE.GAP;
}
