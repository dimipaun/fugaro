// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The contract's document rules over the wire shape (mobile plan, Task P3.2;
// console/docs/json-contract.md §1, §2, §10.8, §10.13).
//
// Two jobs, and they are the two halves of §10.13's first five rules:
//
//   1. **`decode`** -- rule 1: "split stdout on newlines and decode each line as
//      one JSON object; **skip a line that is not**". The phone reads nodes
//      rather than a child's stdout, so the case arrives differently and means
//      the same thing: a node whose value is a bare string, a number, an array
//      or unparseable text is **skipped**, never thrown on and never rendered.
//      §10.8 is why this is a requirement rather than a precaution -- the
//      degraded path writes a plain sentence on the document stream -- and the
//      skipped thing is surfaced as a notice, which is §10.8's "surface it as a
//      header warning".
//
//   2. **`validateDocument`** -- rules 2 and 5: the document is the newest
//      sampled whole, and **a missing field is `unavailable`, never `0`**. Every
//      number in the document comes out of here as a figure
//      (`availability.js`), so nothing downstream can add a missing value to
//      anything, and `null` on the wire (`daily_cap_usd`, `percent`,
//      `usd_per_min` when `known` is false) is `unavailable` rather than zero.
//
// **What is refused, and why the document is judged strictly.** A document is
// the phone's *own* shape: it is built by `nodes.js` from the four granted
// families and then validated here, so an unknown field is a field this reader
// would silently drop, and a document missing a structural field (`project`,
// `now`, `connection.state`) is one whose numbers cannot be trusted to mean
// what they say. Both are refusals. **The nodes are read leniently by
// contrast**: `/spend/<day>/repos/<slug>` carries `byModel` and `calls`, which
// the phone does not read, so `nodes.js` ignores what it does not use and
// judges only what it does -- a tree that grows a field must not break the
// feed, while a document that grows one must not be rendered as if it were
// understood.
//
// **Two fields are the grant's, not the shape's.** `runs`, `finished` and
// `ready` come from the runs bucket, which the phone does not see (the queued
// and finished rows live there, and tension (a)'s resolution says so). They are
// accepted only as **empty** arrays: an empty array is "no rows", which is what
// the phone sees anyway, and a non-empty one is data the grant denies, refused
// here so that no screen can render a run title the phone is not allowed to
// read. `queued_note` is the same family's field and is not read at all.
//
// **The degraded shape is its own shape** (§2 fact (iii), §10.8): the CLI's
// `watchDegradedDoc` is `lsDoc` **embedded** -- `project`, `runs[]`, `totals`,
// `warnings[]` -- plus `degraded` and `notice`, and *none* of `connection`,
// `total` or `repos`, because the document is what the CLI emits when it has no
// budget backend. It is recognised by that key -- "the `degraded` key is the
// discriminator" -- and a document that claims to be degraded and carries the
// live half anyway is refused rather than half-rendered.
//
// **The `lsDoc` half is judged and dropped, not refused.** An earlier version
// of this file accepted only `project`, `degraded` and `notice` and refused
// everything else -- which meant §2 (iii)'s own document was refused as
// `UNKNOWN_FIELD: totals` before the discriminator was ever looked at, so the
// shape this module claimed to recognise was a shape the CLI never emits. The
// two halves are now judged separately, and the `lsDoc` fields are read for
// their **type** and then dropped: this app renders no run row (the grant does
// not cover the runs bucket), so the rows are a fact this document cannot use
// and must not carry. What the view does with a degraded document is
// `view.js`'s rule: degraded renders as degraded, never as a zero spend.

import { isFigure, measured, notional, unavailable } from './availability.js';

/** The refusal and skip codes. The first three are skips (rule 1); the rest
 * are refusals of a document that claimed to be one. */
export const DOCUMENT_CODES = Object.freeze({
  NOT_TEXT: 'not-text',
  NOT_JSON: 'not-json',
  NOT_AN_OBJECT: 'not-an-object',
  MISSING_FIELD: 'missing-field',
  UNKNOWN_FIELD: 'unknown-field',
  BAD_TYPE: 'bad-type',
  BAD_MODE: 'bad-mode',
  BAD_DAY: 'bad-day',
  BAD_STATE: 'bad-state',
  BAD_NUMBER: 'bad-number',
  NOT_GRANTED: 'not-granted',
  CONTRADICTORY: 'contradictory',
});

/** The connection states, read off `internal/watch/json.go`'s `connNames`. */
export const CONNECTION_STATES = Object.freeze(['live', 'stale', 'offline', 'refused', 'polling']);

/** The modes `watch --json` can carry. `budget show --json` is the one document
 * that can carry `""`, and the phone reads no `budget show` document. */
export const MODES = Object.freeze(['observe', 'enforce']);

/** The **live** document's own top-level fields, and the ones the phone reads
 * of them. `degraded` and `notice` are not here: they belong to the other
 * shape, which is judged by `DEGRADED_FIELDS` before this list is ever
 * consulted (`degraded` would not reach it anyway -- it is the discriminator),
 * and a document carrying `notice` and no `degraded` is a document from neither
 * shape. The live JSONDoc has no such field (`internal/cli/testdata/
 * watch_json_keys.golden`). */
const DOC_FIELDS = Object.freeze([
  'project',
  'mode',
  'day',
  'now',
  'connection',
  'total',
  'repos',
  'runs',
  'finished',
  'ready',
  'queued_note',
]);

/** The runs-bucket fields of the **live** document: accepted empty, refused
 * with content. The degraded document is the exception, and §2 (iii) is why:
 * its `runs[]` is `lsDoc`'s own list, bounded by `degradedSince`, and the shape
 * cannot be recognised at all if the field is refused. */
const RUN_BUCKET_FIELDS = Object.freeze(['runs', 'finished', 'ready']);

/** §2 (iii)'s document, in full: `lsDoc` embedded (`internal/cli/runs.go`) plus
 * its two own fields. `totals` and `warnings` are here and **not** in
 * `DOC_FIELDS`, because the live document carries neither -- keeping them apart
 * is what makes "a live document with a `totals` block" an unknown field rather
 * than a shape that slipped in. */
const DEGRADED_FIELDS = Object.freeze([
  'project',
  'runs',
  'totals',
  'warnings',
  'degraded',
  'notice',
]);

const TOTAL_FIELDS = Object.freeze([
  'counted_usd',
  'spent_usd',
  'notional_usd',
  // `leased_usd` -- the money open leases hold and have not yet spent -- is
  // written by `watch --json` now, so it is accepted here like every other
  // field of the shape; the phone reads nothing from it, because no screen
  // draws it yet and a figure nothing renders is a figure nobody checked.
  'leased_usd',
  'daily_cap_usd',
  'percent',
  // `max_parallel` -- the concurrent-run cap a launch is checked against -- is
  // written by `watch --json` now and lives on `total` alone (a repository has
  // no cap of its own, which is why `REPO_FIELDS` does not carry it): accepted
  // here like every other field of the shape, and read by nothing, because no
  // screen draws a cap bar yet. `null` is a document whose writer had none.
  'max_parallel',
  'burn',
  'kill',
  'runs',
  'run_hours',
]);

const REPO_FIELDS = Object.freeze([
  'slug',
  'repo',
  'counted_usd',
  'spent_usd',
  'notional_usd',
  // As on a total: accepted, and read by nothing here.
  'leased_usd',
  'daily_cap_usd',
  'percent',
  'burn',
  'kill',
]);

const BURN_FIELDS = Object.freeze(['known', 'usd_per_min', 'fast']);
const KILL_FIELDS = Object.freeze(['by', 'at', 'reason']);
const CONN_FIELDS = Object.freeze(['state', 'reason', 'age_seconds']);

/** The longest a document's text field may be, so nothing unbounded reaches a
 * screen. The contract's own fields are short (`project`, a mode, a date). */
export const TEXT_MAX = 200;

/** A frozen refusal, in the shape the app's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/** A skip: a line or a node value that is not a JSON object. */
function skip(code) {
  return Object.freeze({ ok: false, skipped: true, code });
}

/**
 * Decode one line of a document stream, or one node's value.
 *
 * A string is parsed; anything else is taken as the value the database
 * returned. Either way the result is `{ ok: true, value }` for an object and a
 * **skip** for everything else -- never a throw, because §10.8's whole point is
 * that a line that is not JSON is expected on this stream.
 *
 * **The skip carries no echo of what it skipped.** The console can show the
 * CLI's own sentence because the CLI produced and sanitised it; a node's
 * contents are something this app did not parse and cannot vouch for, so the
 * notice it surfaces is a static sentence and the line itself is dropped.
 */
export function decode(line) {
  let value = line;
  if (typeof line === 'string') {
    if (line.trim().length === 0) return skip(DOCUMENT_CODES.NOT_JSON);
    try {
      value = JSON.parse(line);
    } catch {
      return skip(DOCUMENT_CODES.NOT_JSON);
    }
  } else if (line === null || typeof line !== 'object') {
    return skip(DOCUMENT_CODES.NOT_TEXT);
  }
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return skip(DOCUMENT_CODES.NOT_AN_OBJECT);
  return Object.freeze({ ok: true, value });
}

/** A bounded, printable string, or `null` when the value is not one. */
function text(value) {
  if (typeof value !== 'string' || value.length === 0 || value.length > TEXT_MAX) return null;
  return value;
}

/**
 * A database-derived string made safe to print, the way the CLI makes one.
 *
 * `internal/watch/build.go`'s `clean` is `clip(safetext.Strip(s))`, and
 * `State.kill` passes **both** `by` and `reason` through it: `safetext.Strip`
 * drops escape sequences (ESC, CSI, OSC, DCS), every other C0/C1 control, the
 * bidi and zero-width characters and the line/paragraph separators, and `clip`
 * bounds the runes that are left. Those two fields are the operator's own tools
 * writing into a database node -- the same untrusted source the CLI is careful
 * about -- and this app's `text()` bounds the length and nothing else, so a
 * right-to-left override or a terminal escape in a kill reason would have
 * reached the screen intact.
 *
 * Stripping rather than refusing is the CLI's own choice, and it is kept here:
 * a kill switch is a state this app must render whatever its `reason` looks
 * like, and the switch's on-ness is the load-bearing part. The character set is
 * `account.js`'s `UNSAFE_TEXT_RE` plus the surrogates a lone half leaves.
 */
const UNSAFE_TEXT_RE = /[\p{Cc}\p{Cf}\p{Cs}\p{Zl}\p{Zp}]/gu;

function cleanText(value) {
  if (typeof value !== 'string') return null;
  return text(value.replace(UNSAFE_TEXT_RE, ''));
}

/** A figure from a wire number: absent, `null` and a non-number are all
 * `unavailable`, and a number is measured. This is rule 5, in one place. */
function numberFigure(value, state = measured) {
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) return unavailable();
  return state(value);
}

function burnOf(value) {
  if (value === undefined || value === null) return Object.freeze({ known: false, usdPerMin: unavailable(), fast: false });
  if (typeof value !== 'object' || Array.isArray(value)) return null;
  for (const key of Object.keys(value)) if (!BURN_FIELDS.includes(key)) return null;
  // `known` is the availability state of `usd_per_min` (the contract's own
  // sentence), so it is read first: `known: false` makes the rate unavailable
  // **even when a number is beside it**, which is the "never 0" rule applied to
  // a rate. A missing `known` reads as not known, which is the safe direction.
  const known = value.known === true;
  return Object.freeze({
    known,
    usdPerMin: known ? numberFigure(value.usd_per_min) : unavailable(),
    fast: value.fast === true,
  });
}

function killOf(value) {
  if (value === undefined || value === null) return null;
  if (typeof value !== 'object' || Array.isArray(value)) return undefined;
  for (const key of Object.keys(value)) if (!KILL_FIELDS.includes(key)) return undefined;
  // All three strings are database-derived -- `by`, `at` and `reason` are the
  // kill node's own fields -- so all three are cleaned, which is what
  // `State.kill` does to the two it prints.
  return Object.freeze({
    on: true,
    by: cleanText(value.by),
    at: cleanText(value.at),
    reason: cleanText(value.reason),
  });
}

/** The figures a total and a repository block share. `where` names the field
 * for the refusal a caller reads. The judgement is `{ ok, figures }` or a
 * refusal -- never a bare figure block, so a caller cannot mistake a payload
 * for a verdict. */
function figuresOf(value, fields, where) {
  for (const key of Object.keys(value)) if (!fields.includes(key)) return refuse(DOCUMENT_CODES.UNKNOWN_FIELD, `${where}.${key}`);
  const burn = burnOf(value.burn);
  if (burn === null) return refuse(DOCUMENT_CODES.BAD_TYPE, `${where}.burn`);
  const kill = killOf(value.kill);
  if (kill === undefined) return refuse(DOCUMENT_CODES.BAD_TYPE, `${where}.kill`);
  return Object.freeze({
    ok: true,
    figures: Object.freeze({
      counted: numberFigure(value.counted_usd),
      spent: numberFigure(value.spent_usd),
      // A subscription's list price: never billed, never capped, and never
      // added to the real total (design §6.2, "one account of cost").
      notional: numberFigure(value.notional_usd, notional),
      dailyCap: numberFigure(value.daily_cap_usd),
      percent: numberFigure(value.percent),
      burn,
      kill,
    }),
  });
}

function repoOf(value, index) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return refuse(DOCUMENT_CODES.BAD_TYPE, `repos[${index}]`);
  }
  for (const key of Object.keys(value)) {
    if (!REPO_FIELDS.includes(key)) return refuse(DOCUMENT_CODES.UNKNOWN_FIELD, `repos[${index}].${key}`);
  }
  const slug = text(value.slug);
  if (slug === null) return refuse(DOCUMENT_CODES.MISSING_FIELD, `repos[${index}].slug`);
  const figures = figuresOf(value, REPO_FIELDS, `repos[${index}]`);
  if (!figures.ok) return figures;
  return Object.freeze({ ok: true, repo: Object.freeze({ slug, repo: text(value.repo), ...figures.figures }) });
}

function connectionOf(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return refuse(DOCUMENT_CODES.MISSING_FIELD, 'connection');
  }
  for (const key of Object.keys(value)) if (!CONN_FIELDS.includes(key)) return refuse(DOCUMENT_CODES.UNKNOWN_FIELD, `connection.${key}`);
  if (!CONNECTION_STATES.includes(value.state)) return refuse(DOCUMENT_CODES.BAD_STATE, 'connection.state');
  const age = value.age_seconds;
  if (typeof age !== 'number' || !Number.isFinite(age) || age < 0) return refuse(DOCUMENT_CODES.BAD_NUMBER, 'connection.age_seconds');
  return Object.freeze({ ok: true, connection: Object.freeze({ state: value.state, reason: text(value.reason), ageSeconds: age }) });
}

/**
 * §2 (iii)'s document: `lsDoc` embedded plus `degraded` and `notice`.
 *
 * Judged **before** the live document's field list, because the discriminator
 * decides which field list applies: `totals` and `warnings` are §2 (iii)'s and
 * the live document has neither, and running the live check first is what made
 * this branch unreachable for the CLI's own document.
 *
 * The `lsDoc` half is read for its **type** and then **dropped**. `runs[]` is
 * the runs bucket's (the grant does not cover it, and this app renders no run
 * title), and `totals` and `warnings` are `ls`'s own reporting, which this
 * document has no field for. Judging their types is what keeps a document that
 * is `lsDoc`-shaped from being a document that merely says `degraded`.
 */
function validateDegraded(value) {
  if (value.degraded !== true) return refuse(DOCUMENT_CODES.BAD_TYPE, 'degraded');
  // The contradiction is judged before the field list, so the refusal names the
  // real fault: a live half beside `degraded` is a document claiming two shapes
  // at once, not a document with an unknown key.
  for (const key of ['connection', 'total', 'repos']) {
    if (value[key] !== undefined) return refuse(DOCUMENT_CODES.CONTRADICTORY, key);
  }
  for (const key of Object.keys(value)) {
    if (!DEGRADED_FIELDS.includes(key)) return refuse(DOCUMENT_CODES.UNKNOWN_FIELD, key);
  }
  const notice = text(value.notice);
  if (notice === null) return refuse(DOCUMENT_CODES.MISSING_FIELD, 'notice');
  if (value.runs !== undefined && !Array.isArray(value.runs)) return refuse(DOCUMENT_CODES.BAD_TYPE, 'runs');
  if (value.warnings !== undefined && !Array.isArray(value.warnings)) return refuse(DOCUMENT_CODES.BAD_TYPE, 'warnings');
  if (value.totals !== undefined && (value.totals === null || typeof value.totals !== 'object' || Array.isArray(value.totals))) {
    return refuse(DOCUMENT_CODES.BAD_TYPE, 'totals');
  }
  return Object.freeze({
    ok: true,
    document: Object.freeze({ degraded: true, notice, project: text(value.project) }),
  });
}

/**
 * Judge one document. Returns `{ ok: true, document }` -- normalised, every
 * number a figure -- or a refusal with the code and the field at fault.
 *
 * A refusal is not a skip: a skip is "this was never a document", and a refusal
 * is "this claimed to be one and is not one this reader can render". The view
 * records both as a notice and renders neither, so the difference is for the
 * caller's diagnostics rather than for the screen.
 */
export function validateDocument(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return refuse(DOCUMENT_CODES.NOT_AN_OBJECT, 'document');
  }

  // The degraded shape first, and by its own field list: it is the one document
  // that carries neither a connection nor a total, and a document that carries
  // both halves is not a shape this reader can render as one thing.
  if (value.degraded !== undefined) return validateDegraded(value);

  for (const key of Object.keys(value)) {
    if (!DOC_FIELDS.includes(key)) return refuse(DOCUMENT_CODES.UNKNOWN_FIELD, key);
  }

  const project = text(value.project);
  if (project === null) return refuse(DOCUMENT_CODES.MISSING_FIELD, 'project');
  // `mode` is **optional**, and absent is `null`: `/config/mode` is not one of
  // the four granted families, so the phone cannot read the mode and must not
  // claim one. The CLI's own document carries it because the CLI reads the
  // config; a phone that filled in `observe` would be stating a fact it does
  // not have, on the one setting that decides whether a cap is enforced.
  if (value.mode !== undefined && !MODES.includes(value.mode)) return refuse(DOCUMENT_CODES.BAD_MODE, 'mode');
  const mode = value.mode === undefined ? null : value.mode;
  // `""` is the contract's "before the first day is set"; the phone's document
  // carries the day its own clock is in, so a missing or empty day is a
  // document whose series this reader cannot place, and it is refused rather
  // than guessed from the clock.
  const day = text(value.day);
  if (day === null || !/^\d{4}-\d{2}-\d{2}$/.test(day)) return refuse(DOCUMENT_CODES.BAD_DAY, 'day');
  const now = text(value.now);
  if (now === null) return refuse(DOCUMENT_CODES.MISSING_FIELD, 'now');

  for (const key of RUN_BUCKET_FIELDS) {
    if (value[key] === undefined) continue;
    if (!Array.isArray(value[key])) return refuse(DOCUMENT_CODES.BAD_TYPE, key);
    if (value[key].length > 0) return refuse(DOCUMENT_CODES.NOT_GRANTED, key);
  }

  const connection = connectionOf(value.connection);
  if (!connection.ok) return connection;

  const total = value.total;
  if (total === null || typeof total !== 'object' || Array.isArray(total)) return refuse(DOCUMENT_CODES.MISSING_FIELD, 'total');
  const figures = figuresOf(total, TOTAL_FIELDS, 'total');
  if (!figures.ok) return figures;
  // `runs` and `run_hours` are the total's alone (a repository block has
  // neither). The phone cannot read either of them -- no granted family counts
  // a day's runs or its hours -- so they are `unavailable` in every document
  // `reader.js`'s `#assemble` builds, which is what "or not shown" means for
  // them.
  const totals = Object.freeze({
    ...figures.figures,
    runs: numberFigure(total.runs),
    runHours: numberFigure(total.run_hours),
  });

  // `repos` absent is **unavailable**, not empty: the contract's `[]` means
  // "none", and a document that did not carry the field says nothing about
  // whether there are repositories.
  let repos = null;
  if (value.repos !== undefined) {
    if (!Array.isArray(value.repos)) return refuse(DOCUMENT_CODES.BAD_TYPE, 'repos');
    const judged = [];
    for (const [index, entry] of value.repos.entries()) {
      const repo = repoOf(entry, index);
      if (!repo.ok) return repo;
      judged.push(repo.repo);
    }
    repos = Object.freeze(judged);
  }

  return Object.freeze({
    ok: true,
    document: Object.freeze({
      degraded: false,
      project,
      mode,
      day,
      now,
      connection: connection.connection,
      total: totals,
      repos,
      queuedNote: text(value.queued_note),
    }),
  });
}

/** Is this a document this module built? (`validateDocument`'s own output.) */
export function isDocument(value) {
  return value !== null && typeof value === 'object' && typeof value.degraded === 'boolean';
}

/** Every figure of a document, in one list, for a caller that must judge them
 * all (the view does, and so does the test that pins "never 0"). */
export function figures(document) {
  if (!isDocument(document)) return Object.freeze([]);
  if (document.degraded) return Object.freeze([]);
  const found = [];
  for (const key of ['counted', 'spent', 'notional', 'dailyCap', 'percent', 'runs', 'runHours']) {
    found.push(document.total[key]);
  }
  found.push(document.total.burn.usdPerMin);
  for (const repo of document.repos ?? []) {
    for (const key of ['counted', 'spent', 'notional', 'dailyCap', 'percent']) {
      found.push(repo[key]);
    }
    found.push(repo.burn.usdPerMin);
  }
  return Object.freeze(found);
}

/** A figure's state, for a test that wants to assert the whole set at once. */
export function isUnavailable(fig) {
  return isFigure(fig) && fig.value === null;
}
