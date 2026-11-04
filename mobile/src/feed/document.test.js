// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The contract's document rules (mobile plan, Task P3.2; json-contract §1,
// §2, §10.8, §10.13 rules 1, 2 and 5).
//
// Three of the plan's named cases live here, because they are rules about a
// document rather than about a screen:
//
//   * **a non-JSON line is skipped** -- §10.13 rule 1, §10.8's plain sentence
//     on the document stream. Skipped, never thrown on, never rendered.
//   * **a missing field becomes `unavailable` and never `0`** -- §1's first
//     rule, asserted for *every* figure of a document and for every way a
//     field can be missing (absent, `null`, the wrong type, `known: false`).
//   * **the degraded shape** -- §2 fact (iii): its own discriminator, and no
//     connection, cap, burn or kill half at all.

import assert from 'node:assert/strict';
import test from 'node:test';

import { STATE, isFigure, measured, shown, unavailable } from './availability.js';
import {
  CONNECTION_STATES,
  DOCUMENT_CODES,
  MODES,
  decode,
  figures,
  isDocument,
  validateDocument,
} from './document.js';

/** A well-formed document, as `nodes.js` assembles one. */
function wire(overrides = {}) {
  return {
    project: 'fugaro',
    mode: 'observe',
    day: '2025-11-09',
    now: '2025-11-09T12:00:00Z',
    connection: { state: 'live', age_seconds: 0 },
    total: {
      counted_usd: 12.5,
      spent_usd: 12.5,
      notional_usd: 40,
      daily_cap_usd: 50,
      percent: 25,
      burn: { known: true, usd_per_min: 0.05, fast: false },
      kill: null,
      runs: 3,
      run_hours: 1.5,
    },
    repos: [],
    ...overrides,
  };
}

/** The plan's degraded sentence, verbatim from `internal/cli/watch.go`'s
 * `runDegraded` -- the line that is not JSON on the document stream. */
const DEGRADED_SENTENCE =
  'no budget backend: showing run records only\nrun records unavailable (retrying): the database refused the read';

/** The degraded document's own notice, verbatim from `internal/cli/watch.go`'s
 * `degradedNotice`. */
const DEGRADED_NOTICE = 'no budget backend: showing run records only';

/**
 * §2 fact (iii)'s document, built the way the CLI builds it.
 *
 * `watchDegradedDoc` is `lsDoc` **embedded** plus two fields, and `lsDoc`
 * (`internal/cli/runs.go`) is `project`, `runs[]`, `totals` and `warnings[]`.
 * The row is `runview.Row`'s shape and the total `runview.Totals`', because
 * that is what the CLI writes on the wire: a fixture with only `project`,
 * `degraded` and `notice` would be a document the CLI never emits, and a
 * validator tested against it proves nothing about the real one.
 */
function degradedDoc(overrides = {}) {
  return {
    project: 'fugaro',
    runs: [
      {
        run: 'run-1',
        repo: 'example-one',
        run_id: 'run-1',
        status: 'running',
        created: '2025-11-09T11:00:00Z',
        cost: { model_usd: 0.2, compute_usd: 0.05, compute_estimated: true, total_usd: 0.25, estimate: false, model_basis: 'api-list' },
        terminal: false,
        settled: false,
        pushed: true,
      },
    ],
    totals: {
      runs: 1,
      model_usd: 0.2,
      model_notional_usd: 0.05,
      compute_usd: 0.05,
      total_usd: 0.25,
      compute_not_estimated: 0,
    },
    warnings: [],
    degraded: true,
    notice: DEGRADED_NOTICE,
    ...overrides,
  };
}

test('a line that is not a JSON object is skipped, and never thrown on', () => {
  for (const line of [
    DEGRADED_SENTENCE,
    'no budget backend: showing run records only',
    'run records unavailable (retrying): timeout',
    '',
    '   ',
    '{ not json',
    '[1, 2, 3]',
    'null',
    '"a bare string"',
    '42',
    'true',
  ]) {
    const decoded = decode(line);
    assert.equal(decoded.ok, false, JSON.stringify(line));
    assert.equal(decoded.skipped, true, JSON.stringify(line));
    assert.ok(Object.values(DOCUMENT_CODES).includes(decoded.code), decoded.code);
    // A skip carries no echo of what it skipped: a node's contents are
    // something this app did not parse, so the notice is static.
    assert.equal(JSON.stringify(decoded).includes('retrying'), false);
  }
  // ... and the codes are the three ways a line fails to be an object.
  assert.equal(decode('{ not json').code, DOCUMENT_CODES.NOT_JSON);
  assert.equal(decode('[1, 2, 3]').code, DOCUMENT_CODES.NOT_AN_OBJECT);
  assert.equal(decode(7).code, DOCUMENT_CODES.NOT_TEXT);
  assert.equal(decode(null).code, DOCUMENT_CODES.NOT_TEXT);
  assert.equal(decode(undefined).code, DOCUMENT_CODES.NOT_TEXT);
});

test('an object arrives as itself, from a line or from a node', () => {
  const value = wire();
  const fromNode = decode(value);
  assert.equal(fromNode.ok, true);
  assert.equal(fromNode.value, value, 'a node value is handed on, not copied');
  const fromLine = decode(JSON.stringify(value));
  assert.deepEqual(fromLine.value, value);
  assert.equal(decode('  {"a": 1}  ').ok, true, 'surrounding whitespace is the transport, not the document');
});

test('a missing field is unavailable, never zero, for every figure of a document', () => {
  // The whole document, with every figure's source field absent.
  const bare = wire({
    total: { burn: { known: false } },
    repos: [{ slug: 'example-one-0123456789abcdef' }],
  });
  const judged = validateDocument(bare);
  assert.equal(judged.ok, true, JSON.stringify(judged));
  const all = figures(judged.document);
  assert.ok(all.length >= 14, `expected every figure, found ${all.length}`);
  for (const fig of all) {
    assert.ok(isFigure(fig), JSON.stringify(fig));
    assert.equal(fig.state, STATE.UNAVAILABLE, JSON.stringify(fig));
    assert.equal(fig.value, null, 'a missing field carries no number at all');
    assert.equal(shown(fig), 'n/a');
    assert.notEqual(shown(fig), '0');
  }
  // The total's figures that only a whole document can carry are in there too.
  assert.equal(shown(judged.document.total.counted), 'n/a');
  assert.equal(shown(judged.document.total.dailyCap), 'n/a');
  assert.equal(shown(judged.document.total.burn.usdPerMin), 'n/a');
  assert.equal(shown(judged.document.repos[0].counted), 'n/a');

  // ... and a *measured* zero is not one of those: `0` on the wire is a reading.
  const zeroed = validateDocument(wire({ total: { ...wire().total, counted_usd: 0 } }));
  assert.equal(zeroed.ok, true);
  assert.equal(shown(zeroed.document.total.counted), '0');
  assert.equal(zeroed.document.total.counted.state, STATE.MEASURED);
});

test('a rate is unavailable when the document says it does not know it', () => {
  // `burn.known` is the availability state of `usd_per_min` (the contract's own
  // sentence), so a number beside `known: false` is not a measurement.
  const judged = validateDocument(wire({ total: { ...wire().total, burn: { known: false, usd_per_min: 9.5, fast: false } } }));
  assert.equal(judged.ok, true);
  assert.equal(shown(judged.document.total.burn.usdPerMin), 'n/a');
  assert.equal(judged.document.total.burn.known, false);
  // `known` absent reads as not known, which is the safe direction.
  const silent = validateDocument(wire({ total: { ...wire().total, burn: { usd_per_min: 9.5 } } }));
  assert.equal(silent.ok, true);
  assert.equal(shown(silent.document.total.burn.usdPerMin), 'n/a');
  // ... and `known: true` with a number is measured.
  const known = validateDocument(wire());
  assert.equal(shown(known.document.total.burn.usdPerMin), '0.05');
});

test('a null cap is unavailable, and a notional total is not a real one', () => {
  const judged = validateDocument(wire({ total: { ...wire().total, daily_cap_usd: null, percent: null } }));
  assert.equal(judged.ok, true);
  assert.equal(shown(judged.document.total.dailyCap), 'n/a');
  assert.equal(shown(judged.document.total.percent), 'n/a');
  assert.equal(judged.document.total.dailyCap.value, null);

  const full = validateDocument(wire());
  assert.equal(shown(full.document.total.notional), 'NOTIONAL~40');
  assert.equal(shown(full.document.total.counted), '12.5');
  // No feed field is estimated: the contract's only estimated field is
  // `report`'s compute, which the phone does not read. A state this app could
  // not produce would otherwise be a state nobody ever checked.
  for (const fig of figures(full.document)) assert.notEqual(fig.state, STATE.ESTIMATED);
});

test('a document carrying leased_usd is read, and nothing is drawn from it', () => {
  // `leased_usd` -- the money open leases hold and have not yet spent -- is on
  // `total` and on every `repos[]` entry now, so a document from this CLI
  // carries a key the phone's own shape did not have; without it named, every
  // live document would be refused whole as `unknown-field`. The phone reads
  // no figure from it, because no screen draws a lease and a figure nothing
  // renders is one nobody checked.
  const repo = {
    slug: 'acme/app', repo: 'acme/app', counted_usd: 1, spent_usd: 0.8, notional_usd: 0,
    leased_usd: 0.2, daily_cap_usd: null, percent: null,
    burn: { known: true, usd_per_min: 0, fast: false }, kill: null,
  };
  const judged = validateDocument(wire({ total: { ...wire().total, leased_usd: 2.5 }, repos: [repo] }));
  assert.equal(judged.ok, true);
  // Nothing about the totals changes, and the repository carries none of it.
  assert.deepEqual(judged.document.total, validateDocument(wire()).document.total);
  assert.equal(Object.hasOwn(judged.document.repos[0], 'leasedUsd'), false);
  assert.equal(judged.document.repos[0].spent.value, 0.8);
});

test('a document carrying max_parallel is read, and nothing is drawn from it', () => {
  // `max_parallel` -- the concurrent-run cap a launch is checked against -- is
  // on `total` now, so a document from this CLI carries a key the phone's own
  // shape did not have; without it named, every live document would be refused
  // whole as `unknown-field`. Nothing is read from it: no screen draws a cap
  // bar, and a figure nothing renders is one nobody checked. `null` is a
  // document whose writer had no cap to report, and it is accepted too.
  const judged = validateDocument(wire({ total: { ...wire().total, max_parallel: 4 } }));
  assert.equal(judged.ok, true);
  assert.deepEqual(judged.document.total, validateDocument(wire()).document.total);
  assert.equal(validateDocument(wire({ total: { ...wire().total, max_parallel: null } })).ok, true);
  // It is the total's alone: a cap is the project's, not a repository's, so a
  // `repos[]` entry carrying it is a document no version of the CLI writes and
  // stays an unknown field there.
  const repo = {
    slug: 'acme/app', repo: 'acme/app', counted_usd: 1, spent_usd: 0.8, notional_usd: 0,
    daily_cap_usd: null, percent: null,
    burn: { known: true, usd_per_min: 0, fast: false }, kill: null,
  };
  assert.equal(validateDocument(wire({ repos: [repo] })).ok, true);
  assert.equal(validateDocument(wire({ repos: [{ ...repo, max_parallel: 4 }] })).code, DOCUMENT_CODES.UNKNOWN_FIELD);
});

test('a kill switch is a fact, and an absent one is not a killed project', () => {
  const killed = validateDocument(
    wire({ total: { ...wire().total, kill: { by: 'operator', at: '2025-11-09T11:00:00Z', reason: 'cap' } } }),
  );
  assert.deepEqual(killed.document.total.kill, { on: true, by: 'operator', at: '2025-11-09T11:00:00Z', reason: 'cap' });
  // `null` is the contract's "not killed" -- a fact, not a missing value.
  assert.equal(validateDocument(wire()).document.total.kill, null);
  const bare = validateDocument(wire({ total: { ...wire().total, kill: {} } }));
  assert.deepEqual(bare.document.total.kill, { on: true, by: null, at: null, reason: null });
});

test('a kill switch\'s strings are cleaned the way the CLI cleans them', () => {
  // The kill node is a database node, and the CLI treats everything the
  // database holds as text that must be made printable: `State.kill` passes
  // `by` and `reason` through `clean() = clip(safetext.Strip(...))`. This
  // module's `text()` only bounds the length, so without the strip a
  // right-to-left override, a zero-width character or a terminal escape in a
  // reason would reach the screen intact -- and `kill.by` and `kill.reason` are
  // drawn.
  const unsafe = 'cap‮evil​\u001b[31m\u0007';
  const judged = validateDocument(
    wire({ total: { ...wire().total, kill: { by: 'operator', at: '2025-11-09T11:00:00Z', reason: unsafe } } }),
  );
  assert.equal(judged.ok, true);
  // `safetext.Strip` **drops** what it rejects (it does not mark it), so what is
  // left is the printable run.
  assert.equal(judged.document.total.kill.reason, 'capevil[31m');
  assert.equal(/[\p{Cc}\p{Cf}\p{Cs}\p{Zl}\p{Zp}]/u.test(judged.document.total.kill.reason), false);
  // A field that is nothing but unsafe characters is a field that is not there,
  // never an empty string on a screen.
  const blank = validateDocument(wire({ total: { ...wire().total, kill: { by: '‮​', reason: '\u0000' } } }));
  assert.deepEqual(blank.document.total.kill, { on: true, by: null, at: null, reason: null });
  // The instant is a database string on this path too, and it is cleaned with
  // them; `at` is never the field a switch's on-ness depends on.
  const stamped = validateDocument(wire({ total: { ...wire().total, kill: { at: '2025-11-09T11:00:00Z​' } } }));
  assert.equal(stamped.document.total.kill.at, '2025-11-09T11:00:00Z');
});

test('the runs bucket is not the phone\'s, and an empty array is not a run title', () => {
  for (const key of ['runs', 'finished', 'ready']) {
    assert.equal(validateDocument(wire({ [key]: [] })).ok, true, `${key}: [] is "no rows"`);
    const refused = validateDocument(wire({ [key]: [{ run: 'run-1', title: 'a run title' }] }));
    assert.equal(refused.ok, false, key);
    assert.equal(refused.code, DOCUMENT_CODES.NOT_GRANTED, key);
    assert.equal(refused.field, key);
  }
});

test('the degraded shape is its own shape, recognised by its own key', () => {
  const judged = validateDocument(degradedDoc());
  assert.equal(judged.ok, true, JSON.stringify(judged));
  assert.equal(judged.document.degraded, true);
  assert.equal(judged.document.notice, DEGRADED_NOTICE);
  // It carries no half of the other shape, and it has no figures at all -- so
  // there is nothing for a screen to render as a zero spend.
  assert.equal(judged.document.total, undefined);
  assert.equal(judged.document.connection, undefined);
  assert.deepEqual(figures(judged.document), []);
  assert.ok(isDocument(judged.document));
  // **The `lsDoc` half is judged and dropped.** The rows are the runs bucket's
  // (`--json`'s `runs[]`, kept by the grant out of this app's reach), so the
  // judged document carries no run title, no repository and no total: the shape
  // is recognised without any of it reaching a screen.
  assert.equal(JSON.stringify(judged.document).includes('run-1'), false);
  assert.equal(JSON.stringify(judged.document).includes('example-one'), false);

  // The two field lists are kept apart: `totals` and `warnings` belong to the
  // degraded document alone, and `notice` belongs to it alone too, so a **live**
  // document carrying any of them is an unknown field rather than a shape that
  // slipped through.
  for (const key of ['totals', 'warnings', 'notice']) {
    const live = validateDocument(wire({ [key]: degradedDoc()[key] }));
    assert.equal(live.code, DOCUMENT_CODES.UNKNOWN_FIELD, key);
  }
  // `degraded` never reaches the live field list -- it is the discriminator,
  // and any value but `true` is refused as the wrong type rather than ignored.
  assert.equal(validateDocument(wire({ degraded: false })).code, DOCUMENT_CODES.BAD_TYPE);

  // A document that claims to be degraded *and* carries the live half is not
  // one thing, and half-rendering it is the bug this refusal prevents.
  for (const key of ['connection', 'total', 'repos']) {
    const contradictory = validateDocument(degradedDoc({ [key]: wire()[key] }));
    assert.equal(contradictory.ok, false, key);
    assert.equal(contradictory.code, DOCUMENT_CODES.CONTRADICTORY, key);
  }
  assert.equal(validateDocument(degradedDoc({ notice: '' })).code, DOCUMENT_CODES.MISSING_FIELD);
  assert.equal(validateDocument(degradedDoc({ degraded: 'yes' })).code, DOCUMENT_CODES.BAD_TYPE);
  // The `lsDoc` fields are judged for their type: `runs[]` and `warnings[]` are
  // arrays and `totals` is an object in the shape the CLI emits.
  assert.equal(validateDocument(degradedDoc({ runs: 'run-1' })).code, DOCUMENT_CODES.BAD_TYPE);
  assert.equal(validateDocument(degradedDoc({ warnings: 'none' })).code, DOCUMENT_CODES.BAD_TYPE);
  assert.equal(validateDocument(degradedDoc({ totals: 0 })).code, DOCUMENT_CODES.BAD_TYPE);
  // ... and §2 (iii)'s document carries no `finished` or `ready` either: those
  // are the live JSONDoc's, not `lsDoc`'s.
  assert.equal(validateDocument(degradedDoc({ finished: [] })).code, DOCUMENT_CODES.UNKNOWN_FIELD);
  // A degraded document with no rows at all is still one (`degradedSince` may
  // bound a day that has no runs in it).
  assert.equal(validateDocument(degradedDoc({ runs: [], warnings: [], totals: { runs: 0 } })).ok, true);
});

test('a document that is not one this reader can render is refused, field by field', () => {
  const cases = [
    [null, DOCUMENT_CODES.NOT_AN_OBJECT],
    [[], DOCUMENT_CODES.NOT_AN_OBJECT],
    ['a string', DOCUMENT_CODES.NOT_AN_OBJECT],
    [{ ...wire(), surprise: 1 }, DOCUMENT_CODES.UNKNOWN_FIELD],
    [wire({ project: '' }), DOCUMENT_CODES.MISSING_FIELD],
    [wire({ mode: '' }), DOCUMENT_CODES.BAD_MODE],
    [wire({ mode: null }), DOCUMENT_CODES.BAD_MODE],
    [wire({ mode: 'enforcing' }), DOCUMENT_CODES.BAD_MODE],
    [wire({ day: '' }), DOCUMENT_CODES.BAD_DAY],
    [wire({ day: '2025-11-9' }), DOCUMENT_CODES.BAD_DAY],
    [wire({ now: '' }), DOCUMENT_CODES.MISSING_FIELD],
    [wire({ connection: undefined }), DOCUMENT_CODES.MISSING_FIELD],
    [wire({ connection: { state: 'fine', age_seconds: 0 } }), DOCUMENT_CODES.BAD_STATE],
    [wire({ connection: { state: 'live' } }), DOCUMENT_CODES.BAD_NUMBER],
    [wire({ connection: { state: 'live', age_seconds: -1 } }), DOCUMENT_CODES.BAD_NUMBER],
    [wire({ connection: { state: 'live', age_seconds: 0, extra: 1 } }), DOCUMENT_CODES.UNKNOWN_FIELD],
    [wire({ total: undefined }), DOCUMENT_CODES.MISSING_FIELD],
    [wire({ total: { ...wire().total, extra: 1 } }), DOCUMENT_CODES.UNKNOWN_FIELD],
    [wire({ total: { ...wire().total, burn: { known: true, usd_per_min: 1, extra: 1 } } }), DOCUMENT_CODES.BAD_TYPE],
    [wire({ total: { ...wire().total, kill: { extra: 1 } } }), DOCUMENT_CODES.BAD_TYPE],
    [wire({ repos: 'none' }), DOCUMENT_CODES.BAD_TYPE],
    [wire({ repos: [{}] }), DOCUMENT_CODES.MISSING_FIELD],
    [wire({ repos: [{ slug: 'x', extra: 1 }] }), DOCUMENT_CODES.UNKNOWN_FIELD],
    [wire({ repos: [null] }), DOCUMENT_CODES.BAD_TYPE],
  ];
  for (const [value, code] of cases) {
    const judged = validateDocument(value);
    assert.equal(judged.ok, false, JSON.stringify(value));
    assert.equal(judged.code, code, JSON.stringify(value));
    assert.equal(judged.skipped, undefined, 'a refusal is not a skip');
  }
  // The two vocabularies are the contract's own lists, not this module's.
  assert.deepEqual([...CONNECTION_STATES], ['live', 'stale', 'offline', 'refused', 'polling']);
  assert.deepEqual([...MODES], ['observe', 'enforce']);
});

test('the mode is optional, because the phone cannot read it', () => {
  // `/config/mode` is not in the grant (`paths.test.js` refuses it), so the
  // phone has no mode to report and says so rather than guessing `observe` --
  // which is the one setting that decides whether a cap is enforced.
  const judged = validateDocument(wire({ mode: undefined }));
  assert.equal(judged.ok, true);
  assert.equal(judged.document.mode, null);
  assert.notEqual(judged.document.mode, 'observe');
  // A mode that *is* there and is not a mode is still refused, and the
  // contract's two modes are still the two.
  assert.equal(validateDocument(wire({ mode: 'enforcing' })).code, DOCUMENT_CODES.BAD_MODE);
  assert.equal(validateDocument(wire({ mode: 'enforce' })).document.mode, 'enforce');
});

test('repos absent is unavailable, and repos empty is empty', () => {
  // The contract's `[]` means "none"; a document that did not carry the field
  // says nothing about whether there are repositories, which is unavailable.
  const absent = validateDocument(wire({ repos: undefined }));
  assert.equal(absent.ok, true);
  assert.equal(absent.document.repos, null);
  const empty = validateDocument(wire({ repos: [] }));
  assert.deepEqual(empty.document.repos, []);
  assert.notEqual(empty.document.repos, null);
});

test('a repository block carries the same figures as the total', () => {
  const slug = 'example-one-0123456789abcdef';
  const judged = validateDocument(
    wire({
      repos: [
        {
          slug,
          repo: 'example/one',
          counted_usd: 3,
          spent_usd: 3,
          notional_usd: 9,
          daily_cap_usd: 10,
          percent: 30,
          burn: { known: true, usd_per_min: 0.01, fast: true },
          kill: null,
        },
      ],
    }),
  );
  assert.equal(judged.ok, true);
  const repo = judged.document.repos[0];
  assert.equal(repo.slug, slug);
  assert.equal(repo.repo, 'example/one');
  assert.equal(shown(repo.counted), '3');
  assert.equal(shown(repo.percent), '30');
  assert.equal(repo.burn.fast, true);
  assert.equal(repo.kill, null);
  // A figure is never the wire's own object: it is this module's.
  assert.ok(isFigure(repo.counted));
  // Every figure of the document: the total's eight (counted, spent, notional,
  // daily cap, percent, runs, run hours, the rate) plus this block's six.
  assert.equal(figures(judged.document).length, 8 + 6);
});

test('a number that is not a number is unavailable, not a measurement', () => {
  // A wire value of the wrong type is the same fact as an absent one: the
  // document does not carry a value this reader can read.
  for (const bad of ['12', {}, [], NaN, Infinity, -1, true]) {
    const judged = validateDocument(wire({ total: { ...wire().total, counted_usd: bad } }));
    assert.equal(judged.ok, true, String(bad));
    assert.equal(shown(judged.document.total.counted), 'n/a', String(bad));
    assert.notEqual(judged.document.total.counted.state, STATE.MEASURED, String(bad));
  }
});

test('the module judges its own output', () => {
  assert.equal(isDocument(validateDocument(wire()).document), true);
  assert.equal(isDocument(validateDocument({ project: 'p', degraded: true, notice: 'n' }).document), true);
  assert.equal(isDocument(wire()), false, 'a wire document is not a validated one');
  assert.equal(isDocument(null), false);
  assert.deepEqual(figures(unavailable()), [], 'a bare figure is not a document');
  assert.deepEqual(figures(null), []);
  assert.equal(isFigure(measured(1)), true);
});

test('a judged document carries the connection itself, not the judge of it', () => {
  // The shape a reader downstream walks: `document.connection.state`, with no
  // wrapper in between. A wrapper left in (the judgement `{ok, connection}`
  // stored as the connection) makes every state read as `undefined`, and a
  // reader that falls back to `live` would report a quiet feed as healthy.
  const document = validateDocument(wire({ connection: { state: 'stale', reason: 'quiet', age_seconds: 50 } })).document;
  assert.deepEqual(document.connection, { state: 'stale', reason: 'quiet', ageSeconds: 50 });
  assert.equal(document.connection.state, 'stale');
  assert.equal(document.connection.ageSeconds, 50);
  assert.equal(document.connection.ok, undefined, 'no judgement wrapper survives into the document');
  // ... and the same for a repository block, whose figures are unwrapped too.
  const withRepo = validateDocument(wire({ repos: [{ slug: 'a-repo-0123456789abcdef', counted_usd: 2 }] })).document;
  assert.equal(withRepo.repos[0].counted.value, 2);
  assert.equal(withRepo.repos[0].ok, undefined);
  assert.equal(withRepo.total.counted.value, 12.5);
  assert.equal(withRepo.total.ok, undefined);
});
