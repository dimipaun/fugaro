// The digest: the contract's documents, classified, and a refusal for
// everything the digest cannot honestly say (mobile plan, Task P1.3).
//
// It reads the shapes `fugaro watch --json` emits and the contract document
// writes down (`internal/watch/json.go`; `console/docs/json-contract.md`), and
// it answers three questions:
//
//   * what happened, as items carrying the document's own fields;
//   * how severe the most severe of them is (`severity.js`'s ladder);
//   * and, for anything the document does not carry, the literal
//     `unavailable` -- never `0`, never a guess.
//
// Two rules from the plan and the reconciliation are the whole shape of this
// module.
//
// **A missing field is `unavailable`, and never `0`.** The contract's reader
// rules say so (`console/docs/json-contract.md` §10.13, F34/F35), and the
// reason is a screen or a voice with no way to show a difference: an absent
// figure rendered as zero is a claim about money that no document made. So
// absence and `null` both become `UNAVAILABLE` here, and the digest carries
// the marker through to whichever consumer speaks it.
//
// **Nothing is coerced.** An unknown field, a wrong type, an oversize string,
// a value outside an enumeration, a finish with nothing to classify it by and
// a document that contradicts itself are all refusals with their own code
// (`errors.js`). This module is the last place where a malformed document can
// be caught before it becomes something a person hears, and the phone's own
// rule for the mailbox is the same one: fail closed.
//
// It is pure: no clock, no randomness, no network, no filesystem, no state
// that survives a call. Two calls on equal documents return equal digests,
// which is what makes the interrupt policy above it (voice V2.2, mobile P3.5)
// a pure function too.
//
// What is deliberately *not* here: the interrupt policy and the three modes
// (`quiet`, `important`, `all`) are voice V2.2's and the mobile voice screen's
// (P3.5); the spoken sentence is the summariser's; and the redactor is beside
// this file rather than inside it, because it is a different kind of thing --
// a function over text, not over a document. It is re-exported here so that
// this directory has one entry point.

import { DigestError, ERROR } from './errors.js';
import { SEVERITY, moreSevere, mostSevere } from './severity.js';

export { DigestError, ERROR } from './errors.js';
export { REDACTED, redact, redactFunc, redactForms } from './redact.js';
export { SEVERITY, SEVERITY_ORDER, moreSevere, mostSevere, severityRank } from './severity.js';

/**
 * What the digest says where the contract says nothing: the value of a field
 * the document does not carry, or carries as `null`. It is a word rather than
 * a `null` on purpose -- a consumer that forgets to handle it speaks the word,
 * which is wrong but honest, where a `null` or a `0` would be spoken as a
 * figure.
 */
export const UNAVAILABLE = 'unavailable';

/**
 * The longest string field the digest will carry, in UTF-8 bytes. A field
 * longer than this is refused (`E_OVERSIZE`), not clipped: clipping a field
 * silently drops whatever was past the cut, and the redactor above this module
 * is the thing allowed to shorten text, deliberately and visibly.
 */
export const MAX_FIELD_BYTES = 4096;

// --- the contract's shapes -------------------------------------------------
//
// Every shape the digest walks, field by field, from `internal/watch/json.go`.
// The tables are what makes fail-closed possible: a field not named here is an
// unknown field, and an unknown field is a refusal rather than something to
// skip. Nothing here is a type's *optionality*: the contract's own reader rule
// is that an absent field means `unavailable` (see the header), so absence is
// handled once, uniformly, by `read` below.

const STRING = 'string';
const NUMBER = 'number';
const BOOLEAN = 'boolean';
const OBJECT = 'object';
const LIST = 'list';

const KILL_FIELDS = Object.freeze({ by: STRING, at: STRING, reason: STRING });
const BURN_FIELDS = Object.freeze({ known: BOOLEAN, usd_per_min: NUMBER, fast: BOOLEAN });
const CONNECTION_FIELDS = Object.freeze({ state: STRING, reason: STRING, age_seconds: NUMBER });
// `leased_usd` is on `total` and on every `repos[]` entry now -- the money
// open leases hold and have not yet spent, which `internal/watch/json.go`
// writes -- so it is named here: accepted, and refused when it carries the
// wrong shape, like every other NUMBER below. The digest produces nothing
// from it: no item, no field on the summary, for the same reason the console's
// schema draws nothing with it yet.
const TOTAL_FIELDS = Object.freeze({
  counted_usd: NUMBER,
  spent_usd: NUMBER,
  notional_usd: NUMBER,
  leased_usd: NUMBER,
  daily_cap_usd: NUMBER,
  percent: NUMBER,
  // `max_parallel` is on `total` alone -- the concurrent-run cap a launch is
  // checked against, which `internal/watch/json.go` writes and a repository
  // has no counterpart of -- so it is named here and not in `REPO_FIELDS`:
  // accepted, and refused when it carries the wrong shape, like every other
  // NUMBER above. The digest produces nothing from it, as it produces nothing
  // from `leased_usd`: nothing downstream reads a cap yet.
  max_parallel: NUMBER,
  burn: OBJECT,
  kill: OBJECT,
  runs: NUMBER,
  run_hours: NUMBER,
});
const REPO_FIELDS = Object.freeze({
  slug: STRING,
  repo: STRING,
  counted_usd: NUMBER,
  spent_usd: NUMBER,
  notional_usd: NUMBER,
  leased_usd: NUMBER,
  daily_cap_usd: NUMBER,
  percent: NUMBER,
  burn: OBJECT,
  kill: OBJECT,
});
const RUN_FIELDS = Object.freeze({
  run: STRING,
  slug: STRING,
  repo: STRING,
  title: STRING,
  stage: STRING,
  round: STRING,
  verify: STRING,
  models: STRING,
  recipe: STRING,
  auth: STRING,
  notional: BOOLEAN,
  spent_usd: NUMBER,
  age_seconds: NUMBER,
  health: STRING,
  deadline: STRING,
  halted: STRING,
  queued: BOOLEAN,
  stuck: BOOLEAN,
  workflow: STRING,
  requested_by: STRING,
});
const FINISHED_FIELDS = Object.freeze({
  run: STRING,
  slug: STRING,
  repo: STRING,
  title: STRING,
  status: STRING,
  outcome: STRING,
  failed: BOOLEAN,
  pr_url: STRING,
  pr_number: NUMBER,
  age_seconds: NUMBER,
});
const DOC_FIELDS = Object.freeze({
  project: STRING,
  mode: STRING,
  day: STRING,
  now: STRING,
  connection: OBJECT,
  total: OBJECT,
  repos: LIST,
  runs: LIST,
  finished: LIST,
  ready: LIST,
  queued_note: STRING,
});

/**
 * The runstore's statuses, and whether each is a failure. The truth table is
 * `internal/watch/finished.go`'s `isFailure` -- `failed`, `halted` and
 * `infra_error` fail, and `cancelled` does not -- so the phone and the CLI
 * cannot disagree about which finish interrupts. A status outside this table
 * is refused rather than guessed at.
 */
const FINISH_STATUS = Object.freeze({
  succeeded: false,
  failed: true,
  infra_error: true,
  cancelled: false,
  halted: true,
});

/** `JSONRun.Health`'s own three values. */
const HEALTH = Object.freeze({ ok: true, silent: true, lost: true });

/** `JSONRun.Deadline`'s own three values. */
const DEADLINE = Object.freeze({ ok: true, near: true, over: true });

// --- reading a document ----------------------------------------------------

const utf8 = new TextEncoder();

function bytesOf(text) {
  return utf8.encode(text).length;
}

/**
 * Refuse a value that is neither absent nor of the shape the contract names,
 * and return a **snapshot** of it: a null-prototype object holding each known
 * field's value, read from the document exactly once. Everything after this
 * reads the snapshot and never the document, so a getter cannot pass a check
 * and then answer with something else.
 *
 * `null` and `undefined` are the contract's own encoding of "no value" (the
 * Go structs' `*float64` fields marshal to `null`), so they are not a type
 * error; they become `UNAVAILABLE` at the read below. A field not in the
 * schema's **own** properties is unknown -- `constructor` and `toString` are
 * unknown fields, not members of the contract -- and the refusal names the
 * object's path, never the document's key text.
 */
function snapshot(value, schema, path) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    throw new DigestError(ERROR.TYPE, path);
  }
  const out = Object.create(null);
  for (const [field, got] of Object.entries(value)) {
    if (!Object.hasOwn(schema, field)) {
      throw new DigestError(ERROR.UNKNOWN_FIELD, path);
    }
    const spec = schema[field];
    if (got === null || got === undefined) {
      continue;
    }
    const ok = spec === LIST ? Array.isArray(got) : typeof got === spec;
    if (!ok) {
      throw new DigestError(ERROR.TYPE, `${path}.${field}`);
    }
    if (spec === NUMBER && (!Number.isFinite(got) || got < 0)) {
      throw new DigestError(ERROR.RANGE, `${path}.${field}`);
    }
    if (spec === STRING && bytesOf(got) > MAX_FIELD_BYTES) {
      throw new DigestError(ERROR.OVERSIZE, `${path}.${field}`);
    }
    out[field] = got;
  }
  return out;
}

/** A field's value, or `UNAVAILABLE` where the document carries none. */
function read(object, field) {
  const value = object[field];
  return value === undefined || value === null ? UNAVAILABLE : value;
}

/** A field's value as an object, or `null` where the document carries none. */
function readObject(object, field) {
  const value = object[field];
  return value === undefined || value === null ? null : value;
}

/** A field's value as a list, or `[]` where the document carries none. */
function readList(object, field) {
  const value = object[field];
  return value === undefined || value === null ? [] : value;
}

/**
 * Walk a list by index. `Array#forEach` skips the holes of a sparse array, so
 * a document with a hole would be digested as if the entry were not there;
 * the index loop reads the hole as `undefined`, which the entry's own shape
 * check refuses.
 */
function each(list, fn) {
  const length = list.length;
  for (let i = 0; i < length; i++) {
    fn(list[i], i);
  }
}

/** A value that must be one of an enumeration's members, or `UNAVAILABLE`. */
function readEnum(object, field, table, path) {
  const value = read(object, field);
  if (value === UNAVAILABLE) {
    return UNAVAILABLE;
  }
  if (!Object.hasOwn(table, value)) {
    throw new DigestError(ERROR.ENUM_UNKNOWN, `${path}.${field}`);
  }
  return value;
}

// --- the items -------------------------------------------------------------
//
// The order is fixed and documented, so a consumer can rely on it and a test
// can assert it without sorting: the project's own triggers first, then each
// repository's in the document's order, then the ready finishes, then the
// other finishes (each run once -- see `digest`), then the live runs. Nothing
// here sorts by time: the digest holds no clock.

function killItem(object, scope, repo, path) {
  const kill = snapshot(object, KILL_FIELDS, path);
  return {
    kind: 'kill',
    severity: SEVERITY.kill,
    scope,
    repo,
    by: read(kill, 'by'),
    at: read(kill, 'at'),
    reason: read(kill, 'reason'),
  };
}

/**
 * A burn's rate. `known: false` says the CLI could not compute one, so a
 * `usd_per_min` carried beside it is an absent figure spoken as a number
 * (`0`); it is `unavailable`, whatever the document put there.
 */
function burnRate(burn) {
  return read(burn, 'known') === false ? UNAVAILABLE : read(burn, 'usd_per_min');
}

function burnItem(burn, scope, repo) {
  return {
    kind: 'fast_burn',
    severity: SEVERITY.fast_burn,
    scope,
    repo,
    known: read(burn, 'known'),
    usd_per_min: burnRate(burn),
  };
}

/**
 * Whether a finished entry failed, from the two fields that say so. If they
 * disagree, or neither is carried, the digest refuses: those are the two ways
 * a finish can be unclassifiable, and guessing would colour a screen or a
 * sentence with a severity no document supports.
 */
function finishFailed(entry, path) {
  const failed = read(entry, 'failed');
  const status = readEnum(entry, 'status', FINISH_STATUS, path);
  if (failed === UNAVAILABLE && status === UNAVAILABLE) {
    throw new DigestError(ERROR.UNCLASSIFIED, path);
  }
  const byStatus = status === UNAVAILABLE ? UNAVAILABLE : FINISH_STATUS[status];
  if (failed !== UNAVAILABLE && byStatus !== UNAVAILABLE && failed !== byStatus) {
    throw new DigestError(ERROR.CONTRADICTION, path);
  }
  return failed === UNAVAILABLE ? byStatus : failed;
}

function finishedItem(object, scope, path) {
  const entry = snapshot(object, FINISHED_FIELDS, path);
  const failed = finishFailed(entry, path);
  return {
    kind: failed ? 'failed_finish' : scope,
    severity: failed ? SEVERITY.failed : SEVERITY.info,
    repo: read(entry, 'repo'),
    run: read(entry, 'run'),
    title: read(entry, 'title'),
    status: read(entry, 'status'),
    outcome: read(entry, 'outcome'),
    failed: read(entry, 'failed'),
    age_seconds: read(entry, 'age_seconds'),
  };
}

function runItem(object, path) {
  // The shape is checked before anything is read out of the entry: a `null`
  // or non-object element in a list is a refusal with a code, and reading a
  // field first would raise a raw TypeError instead.
  const entry = snapshot(object, RUN_FIELDS, path);
  readEnum(entry, 'health', HEALTH, path);
  readEnum(entry, 'deadline', DEADLINE, path);
  return {
    kind: 'run',
    severity: SEVERITY.info,
    repo: read(entry, 'repo'),
    run: read(entry, 'run'),
    title: read(entry, 'title'),
    stage: read(entry, 'stage'),
    health: read(entry, 'health'),
    deadline: read(entry, 'deadline'),
    spent_usd: read(entry, 'spent_usd'),
    age_seconds: read(entry, 'age_seconds'),
    notional: read(entry, 'notional'),
  };
}

function burnSummary(burn) {
  return {
    known: read(burn, 'known'),
    usd_per_min: burnRate(burn),
    fast: read(burn, 'fast'),
  };
}

/**
 * The digest of one document: the project it belongs to, the items, and the
 * severity of the most severe of them.
 *
 * It refuses a document that is not a document (`E_NOT_A_DOCUMENT`) and every
 * malformed field inside one, as `errors.js` lists. It is a pure function of
 * its argument.
 */
export function digest(input) {
  if (input === null || typeof input !== 'object' || Array.isArray(input)) {
    throw new DigestError(ERROR.NOT_A_DOCUMENT);
  }
  const document = snapshot(input, DOC_FIELDS, '$');

  const items = [];

  const totalDocument = readObject(document, 'total');
  let total = UNAVAILABLE;
  if (totalDocument !== null) {
    const totals = snapshot(totalDocument, TOTAL_FIELDS, '$.total');
    const burnDocument = readObject(totals, 'burn');
    const burn = burnDocument === null ? null : snapshot(burnDocument, BURN_FIELDS, '$.total.burn');
    total = {
      counted_usd: read(totals, 'counted_usd'),
      spent_usd: read(totals, 'spent_usd'),
      notional_usd: read(totals, 'notional_usd'),
      daily_cap_usd: read(totals, 'daily_cap_usd'),
      percent: read(totals, 'percent'),
      runs: read(totals, 'runs'),
      run_hours: read(totals, 'run_hours'),
      burn: burn === null ? UNAVAILABLE : burnSummary(burn),
    };
    const kill = readObject(totals, 'kill');
    if (kill !== null) {
      items.push(killItem(kill, 'project', UNAVAILABLE, '$.total.kill'));
    }
    if (burn !== null && read(burn, 'fast') === true) {
      items.push(burnItem(burn, 'project', UNAVAILABLE));
    }
  }

  const connection = readObject(document, 'connection');
  if (connection !== null) {
    snapshot(connection, CONNECTION_FIELDS, '$.connection');
  }

  const repos = readList(document, 'repos');
  each(repos, (entry, i) => {
    const path = `$.repos[${i}]`;
    const repo = snapshot(entry, REPO_FIELDS, path);
    const slug = read(repo, 'slug');
    const kill = readObject(repo, 'kill');
    if (kill !== null) {
      items.push(killItem(kill, 'repo', slug, `${path}.kill`));
    }
    // The burn's shape is checked whether or not it is fast: a malformed
    // burn is a refusal, not something skipped because it raises nothing.
    const burnDocument = readObject(repo, 'burn');
    if (burnDocument !== null) {
      const burn = snapshot(burnDocument, BURN_FIELDS, `${path}.burn`);
      if (read(burn, 'fast') === true) {
        items.push(burnItem(burn, 'repo', slug));
      }
    }
  });

  // `ready` is a **subset of** `finished`: the contract keeps both lists
  // (`internal/watch/json.go`'s `JSONDoc.Finished` and `.Ready`, the latter
  // derived by `internal/watch/filter.go` as the finished runs whose outcome
  // is ready). So the same run arrives in both, and emitting it twice would
  // make a speaking consumer say it twice and a counting one count it twice.
  // The run is emitted once, at the position of the first list that carries
  // it (`ready` is walked first), but with the **more severe** of its
  // entries: keeping the first blindly would let a `succeeded` copy hide a
  // `failed` one. Equal severity keeps the first. An entry whose `run` is
  // `unavailable` cannot be identified, so it is not deduped against
  // anything.
  const identified = new Map();
  const finishedItems = (entries, kind, listPath) => {
    each(entries, (entry, i) => {
      const item = finishedItem(entry, kind, `${listPath}[${i}]`);
      if (item.run !== UNAVAILABLE) {
        if (identified.has(item.run)) {
          const at = identified.get(item.run);
          if (moreSevere(item.severity, items[at].severity)) {
            items[at] = item;
          }
          return;
        }
        identified.set(item.run, items.length);
      }
      items.push(item);
    });
  };
  finishedItems(readList(document, 'ready'), 'ready', '$.ready');
  finishedItems(readList(document, 'finished'), 'finished', '$.finished');

  each(readList(document, 'runs'), (entry, i) => {
    items.push(runItem(entry, `$.runs[${i}]`));
  });

  return {
    project: read(document, 'project'),
    total,
    severity: mostSevere(items.map((item) => item.severity)),
    items,
  };
}
