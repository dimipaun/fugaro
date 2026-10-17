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
// a function over text, not over a document.

import { DigestError, ERROR } from './errors.js';
import { SEVERITY, mostSevere } from './severity.js';

export { DigestError, ERROR } from './errors.js';
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
const TOTAL_FIELDS = Object.freeze({
  counted_usd: NUMBER,
  spent_usd: NUMBER,
  notional_usd: NUMBER,
  daily_cap_usd: NUMBER,
  percent: NUMBER,
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
 * Refuse a value that is neither absent nor of the shape the contract names.
 * `null` and `undefined` are the contract's own encoding of "no value" (the
 * Go structs' `*float64` fields marshal to `null`), so they are not a type
 * error; they become `UNAVAILABLE` at the read below.
 */
function checkShape(value, schema, path) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    throw new DigestError(ERROR.TYPE, path);
  }
  for (const [field, got] of Object.entries(value)) {
    const spec = schema[field];
    if (spec === undefined) {
      throw new DigestError(ERROR.UNKNOWN_FIELD, `${path}.${field}`);
    }
    if (got === null || got === undefined) {
      continue;
    }
    const ok = spec === LIST ? Array.isArray(got) : typeof got === spec;
    if (!ok) {
      throw new DigestError(ERROR.TYPE, `${path}.${field}`);
    }
    if (spec === STRING && bytesOf(got) > MAX_FIELD_BYTES) {
      throw new DigestError(ERROR.OVERSIZE, `${path}.${field}`);
    }
  }
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
// repository's in the document's order, then the finishes, then the live runs.
// Nothing here sorts by time: the digest holds no clock.

function killItem(object, scope, repo, path) {
  checkShape(object, KILL_FIELDS, path);
  return {
    kind: 'kill',
    severity: SEVERITY.kill,
    scope,
    repo,
    by: read(object, 'by'),
    at: read(object, 'at'),
    reason: read(object, 'reason'),
  };
}

function burnItem(object, scope, repo, path) {
  checkShape(object, BURN_FIELDS, path);
  return {
    kind: 'fast_burn',
    severity: SEVERITY.fast_burn,
    scope,
    repo,
    known: read(object, 'known'),
    usd_per_min: read(object, 'usd_per_min'),
  };
}

/**
 * Whether a finished entry failed, from the two fields that say so. If they
 * disagree, or neither is carried, the digest refuses: those are the two ways
 * a finish can be unclassifiable, and guessing would colour a screen or a
 * sentence with a severity no document supports.
 */
function finishFailed(object, path) {
  const failed = read(object, 'failed');
  const status = readEnum(object, 'status', FINISH_STATUS, path);
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
  checkShape(object, FINISHED_FIELDS, path);
  const failed = finishFailed(object, path);
  return {
    kind: failed ? 'failed_finish' : scope,
    severity: failed ? SEVERITY.failed : SEVERITY.info,
    repo: read(object, 'repo'),
    run: read(object, 'run'),
    title: read(object, 'title'),
    status: read(object, 'status'),
    outcome: read(object, 'outcome'),
    failed: read(object, 'failed'),
    age_seconds: read(object, 'age_seconds'),
  };
}

function runItem(object, repo, path) {
  checkShape(object, RUN_FIELDS, path);
  readEnum(object, 'health', HEALTH, path);
  readEnum(object, 'deadline', DEADLINE, path);
  return {
    kind: 'run',
    severity: SEVERITY.info,
    repo,
    run: read(object, 'run'),
    title: read(object, 'title'),
    stage: read(object, 'stage'),
    health: read(object, 'health'),
    deadline: read(object, 'deadline'),
    spent_usd: read(object, 'spent_usd'),
    age_seconds: read(object, 'age_seconds'),
    notional: read(object, 'notional'),
  };
}

function burnSummary(object) {
  if (object === null) {
    return UNAVAILABLE;
  }
  checkShape(object, BURN_FIELDS, '$.total.burn');
  return {
    known: read(object, 'known'),
    usd_per_min: read(object, 'usd_per_min'),
    fast: read(object, 'fast'),
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
export function digest(document) {
  if (document === null || typeof document !== 'object' || Array.isArray(document)) {
    throw new DigestError(ERROR.NOT_A_DOCUMENT);
  }
  checkShape(document, DOC_FIELDS, '$');

  const items = [];

  const totalDocument = readObject(document, 'total');
  let total = UNAVAILABLE;
  if (totalDocument !== null) {
    checkShape(totalDocument, TOTAL_FIELDS, '$.total');
    total = {
      counted_usd: read(totalDocument, 'counted_usd'),
      spent_usd: read(totalDocument, 'spent_usd'),
      notional_usd: read(totalDocument, 'notional_usd'),
      daily_cap_usd: read(totalDocument, 'daily_cap_usd'),
      percent: read(totalDocument, 'percent'),
      runs: read(totalDocument, 'runs'),
      run_hours: read(totalDocument, 'run_hours'),
      burn: burnSummary(readObject(totalDocument, 'burn')),
    };
    const kill = readObject(totalDocument, 'kill');
    if (kill !== null) {
      items.push(killItem(kill, 'project', UNAVAILABLE, '$.total.kill'));
    }
    const burn = readObject(totalDocument, 'burn');
    if (burn !== null && read(burn, 'fast') === true) {
      items.push(burnItem(burn, 'project', UNAVAILABLE, '$.total.burn'));
    }
  }

  const connection = readObject(document, 'connection');
  if (connection !== null) {
    checkShape(connection, CONNECTION_FIELDS, '$.connection');
  }

  const repos = readList(document, 'repos');
  repos.forEach((repo, i) => {
    const path = `$.repos[${i}]`;
    checkShape(repo, REPO_FIELDS, path);
    const slug = read(repo, 'slug');
    const kill = readObject(repo, 'kill');
    if (kill !== null) {
      items.push(killItem(kill, 'repo', slug, `${path}.kill`));
    }
    const burn = readObject(repo, 'burn');
    if (burn !== null && read(burn, 'fast') === true) {
      items.push(burnItem(burn, 'repo', slug, `${path}.burn`));
    }
  });

  readList(document, 'ready').forEach((entry, i) => {
    items.push(finishedItem(entry, 'ready', `$.ready[${i}]`));
  });

  readList(document, 'finished').forEach((entry, i) => {
    items.push(finishedItem(entry, 'finished', `$.finished[${i}]`));
  });

  readList(document, 'runs').forEach((entry, i) => {
    items.push(runItem(entry, read(entry, 'repo'), `$.runs[${i}]`));
  });

  return {
    project: read(document, 'project'),
    total,
    severity: mostSevere(items.map((item) => item.severity)),
    items,
  };
}
