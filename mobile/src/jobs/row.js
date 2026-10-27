// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The project's row: the one row the read grant lets this list draw
// (mobile plan, Task P3.3; resolved 2026-10-17, tension (b1)).
//
// **What a row is.** Numbers and states, named one by one: the day's spend
// (`spend/$day/global`), the project's cap and the two figures recomputed from
// it (`config/caps/global`, P3.2's `recompute.js`), and the project's kill
// switch (`config/kill/global`). Nothing else is on it. The names are
// `fields.js`'s `ROW_FIELDS`, and this file is the only place that reads a
// document to fill them, so "the row carries the granted fields and no others"
// is a property of one function rather than of every screen.
//
// **Why the document, and not the nodes.** The document P3.2 assembles is
// already the granted families added up and judged: every number in it is a
// **figure** -- a value with an availability state -- produced by
// `src/feed/document.js` from the nodes it read. Reading the nodes again here
// would be a second reader over the same tree with its own opinion about what
// absent means, which is precisely the divergence P3.2's header warns about.
// So this module consumes `src/feed/index.js` and never a node.
//
// **A missing field is `unavailable`, never `0`, and this file does not have to
// enforce that** -- it is `document.js`'s rule, applied once where the numbers
// are read, and `availability.js` cannot hold a number on an `unavailable`
// figure at all. What this file *does* enforce is the next step: every field it
// puts on the row must be a figure the feed built (or a kill state), because a
// row carrying something else is a row `lines.js` cannot print without
// inventing a mark. A field that is not one is a refusal, not a coerced value.
//
// **What a caller gets, and what it means.** `burn.shown` is the plan's
// "recomputed ... or not shown": P3.2's document carries `burn.known` (the
// contract's own availability state for `usd_per_min`), so a window that cannot
// speak yet -- no samples, a span under the warmup, a counter that went
// backwards -- is **not shown** and carries no number, never a `0` per minute.
// `percent` is the cap fraction, recomputed by the feed from **counted** and
// the cap; a cap nobody has set leaves it `unavailable`, never 0%.

import { isFigure, isDocument, STATE } from '../feed/index.js';
import { LIST_CODES, ROW_FIELDS } from './fields.js';

/** A frozen refusal, in the shape the app's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * The document's kill switch as the row carries it: `{ on, by, at, reason }`,
 * always present, because "not killed" is a state and not a missing value.
 *
 * The document's `kill` is `null` for a switch that is off -- `jsonKill`
 * returns a nil pointer and **presence is the on-ness** (contract §2) -- so a
 * `null` here is `off`, which is the contract's own reading and not this
 * module's invention. `by`, `at` and `reason` are the kill node's own strings
 * and carry whatever the document carries: `document.js` has already cleaned
 * them (`clean`/`safetext.Strip`, the same rule `State.kill` applies), so
 * nothing database-derived reaches the screen raw.
 */
export function killState(kill) {
  if (kill === null || kill === undefined) {
    return Object.freeze({ on: false, by: null, at: null, reason: null });
  }
  if (typeof kill !== 'object' || Array.isArray(kill)) return refuse(LIST_CODES.BAD_ROW, 'kill');
  // `on` is read as the document's boolean, and anything else is off -- which
  // is the direction `document.js` already took when it built the block. The
  // two words the screen prints are `KILL_STATES`, and `lines.js` is what
  // chooses between them.
  const on = kill.on === true;
  for (const field of ['by', 'at', 'reason']) {
    if (kill[field] !== null && kill[field] !== undefined && typeof kill[field] !== 'string') {
      return refuse(LIST_CODES.BAD_ROW, `kill.${field}`);
    }
  }
  return Object.freeze({
    on,
    by: kill.by ?? null,
    at: kill.at ?? null,
    reason: kill.reason ?? null,
  });
}

/** The document's burn block as the row carries it. `known` is the contract's
 * availability state of `usd_per_min` (document.js reads it first, so a
 * `known: false` beside a number is still "not shown"), and a burn block the
 * feed did not build is a refusal. */
function burnState(burn) {
  if (burn === null || typeof burn !== 'object' || Array.isArray(burn)) {
    return refuse(LIST_CODES.BAD_ROW, 'burn');
  }
  if (!isFigure(burn.usdPerMin)) return refuse(LIST_CODES.BAD_ROW, 'burn.usdPerMin');
  const known = burn.known === true;
  // A rate the feed marked known must be a measurement: `known` is a claim
  // about a number being there, and a `known: true` over an `unavailable`
  // figure is a document contradicting itself, which this module refuses
  // rather than rendering as "not shown".
  if (known && burn.usdPerMin.state !== STATE.MEASURED) {
    return refuse(LIST_CODES.BAD_ROW, 'burn.usdPerMin');
  }
  return Object.freeze({ shown: known, usdPerMin: burn.usdPerMin, fast: burn.fast === true });
}

/**
 * The project's row, from P3.2's document.
 *
 * Returns `{ ok: true, row }` or a refusal. The document must be one this app
 * built and a **live** one: the degraded shape carries no figures at all
 * (§2 (iii)), and the list renders it as degraded rather than as a row of
 * zeros (`list.js`'s rule, and §10.8's).
 */
export function projectRow(document) {
  if (!isDocument(document)) return refuse(LIST_CODES.BAD_DOCUMENT, 'document');
  if (document.degraded === true) return refuse(LIST_CODES.BAD_DOCUMENT, 'degraded');
  const total = document.total;
  if (total === null || typeof total !== 'object' || Array.isArray(total)) {
    return refuse(LIST_CODES.BAD_DOCUMENT, 'total');
  }
  const figures = {};
  for (const name of ['counted', 'spent', 'notional', 'dailyCap', 'percent']) {
    if (!isFigure(total[name])) return refuse(LIST_CODES.BAD_ROW, name);
    figures[name] = total[name];
  }
  // `ok === false` rather than `!ok`: the two helpers answer with a refusal
  // **or with the value**, and a value has no `ok` key at all -- so a truthiness
  // test would read a sound kill switch as a refusal and hand the caller the
  // switch instead of the row.
  const burn = burnState(total.burn);
  if (burn.ok === false) return burn;
  const kill = killState(total.kill);
  if (kill.ok === false) return kill;
  // The row is built from the table and nothing else: a key not in it would be
  // a field the screen prints without a label, and a field of the table the
  // document does not carry is caught above.
  const row = {};
  for (const name of ROW_FIELDS) {
    row[name] = name === 'burn' ? burn : name === 'kill' ? kill : figures[name];
  }
  return Object.freeze({ ok: true, row: Object.freeze(row) });
}
