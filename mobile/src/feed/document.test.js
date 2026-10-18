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
 * `runWatchDegraded` -- the line that is not JSON on the document stream. */
const DEGRADED_SENTENCE =
  'no budget backend: showing run records only\nrun records unavailable (retrying): the database refused the read';

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
  const degraded = { project: 'fugaro', degraded: true, notice: 'no budget backend: showing run records only' };
  const judged = validateDocument(degraded);
  assert.equal(judged.ok, true);
  assert.equal(judged.document.degraded, true);
  assert.equal(judged.document.notice, 'no budget backend: showing run records only');
  // It carries no half of the other shape, and it has no figures at all -- so
  // there is nothing for a screen to render as a zero spend.
  assert.equal(judged.document.total, undefined);
  assert.equal(judged.document.connection, undefined);
  assert.deepEqual(figures(judged.document), []);
  assert.ok(isDocument(judged.document));

  // A document that claims to be degraded *and* carries the other half is not
  // one thing, and half-rendering it is the bug this refusal prevents.
  for (const key of ['connection', 'total', 'repos']) {
    const contradictory = validateDocument({ ...degraded, [key]: wire()[key] });
    assert.equal(contradictory.ok, false, key);
    assert.equal(contradictory.code, DOCUMENT_CODES.CONTRADICTORY, key);
  }
  assert.equal(validateDocument({ ...degraded, notice: '' }).code, DOCUMENT_CODES.MISSING_FIELD);
  assert.equal(validateDocument({ project: 'fugaro', degraded: 'yes', notice: 'x' }).code, DOCUMENT_CODES.BAD_TYPE);
});

test('a document that is not one this reader can render is refused, field by field', () => {
  const cases = [
    [null, DOCUMENT_CODES.NOT_AN_OBJECT],
    [[], DOCUMENT_CODES.NOT_AN_OBJECT],
    ['a string', DOCUMENT_CODES.NOT_AN_OBJECT],
    [{ ...wire(), surprise: 1 }, DOCUMENT_CODES.UNKNOWN_FIELD],
    [wire({ project: '' }), DOCUMENT_CODES.MISSING_FIELD],
    [wire({ mode: '' }), DOCUMENT_CODES.BAD_MODE],
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
