// The digest's own tests (mobile plan, Task P1.3) -- written BEFORE the module
// they import, which is this package's rule for the first file of a module
// (`shared/README.md`): the red run is the evidence that the runner's glob
// found them at all.
//
// What they pin, in the plan's own words:
//
//   * "a kill is the highest severity; a failed finish is next; a fast burn is
//     next; nothing else interrupts" -- over an exhaustive table of the three
//     triggers, in both places each of them can appear;
//   * "a missing field is `unavailable` and **never** spoken as `0`";
//   * "the severity rule is asserted as a total order over an exhaustive input
//     table" (the order itself is `severity.test.js`);
//   * fail closed: an unknown field, a wrong type, an oversize field and every
//     ambiguity is a refusal with its own code, never a coercion.
//
// The documents below are the contract's shapes (`internal/watch/json.go`'s
// `JSONDoc`, `JSONRun`, `JSONFinished`, `JSONTotal`, `JSONBurn`, `JSONKill`,
// written down in `console/docs/json-contract.md`). They are built here rather
// than recorded from an installation: no fixture is ever recorded from
// `belong` (Global Constraint 11), and a pure module needs no installation.

import assert from 'node:assert/strict';
import test from 'node:test';

import { DigestError, ERROR, SEVERITY, UNAVAILABLE, digest } from './index.js';

// --- the contract's documents, as small builders -------------------------

function burn(over = {}) {
  return { known: true, usd_per_min: 0.05, fast: false, ...over };
}

function kill(over = {}) {
  return { by: 'operator', at: '2026-10-17T17:00:00Z', reason: 'over cap', ...over };
}

function total(over = {}) {
  return {
    counted_usd: 1,
    spent_usd: 1,
    notional_usd: 0,
    daily_cap_usd: 20,
    percent: 0.05,
    burn: burn(),
    kill: null,
    runs: 1,
    run_hours: 0.5,
    ...over,
  };
}

function repo(over = {}) {
  return {
    slug: 's1',
    repo: 'acme/app',
    counted_usd: 1,
    spent_usd: 1,
    notional_usd: 0,
    daily_cap_usd: 20,
    percent: 0.05,
    burn: burn(),
    kill: null,
    ...over,
  };
}

function run(over = {}) {
  return {
    run: 'r1',
    slug: 's1',
    repo: 'acme/app',
    title: 'a live run',
    stage: 'running',
    round: '-',
    verify: '-',
    models: 'deepseek',
    auth: 'api-key',
    notional: false,
    spent_usd: 0.5,
    age_seconds: 30,
    health: 'ok',
    deadline: 'ok',
    ...over,
  };
}

function finished(over = {}) {
  return {
    run: 'r1',
    slug: 's1',
    repo: 'acme/app',
    title: 'a finished run',
    status: 'succeeded',
    outcome: 'ready',
    failed: false,
    age_seconds: 600,
    ...over,
  };
}

function doc(over = {}) {
  return {
    project: 'fugaro',
    mode: 'live',
    day: '2026-10-17',
    now: '2026-10-17T18:00:00Z',
    connection: { state: 'live', age_seconds: 1 },
    total: total(),
    repos: [],
    runs: [],
    finished: [],
    ready: [],
    ...over,
  };
}

function refusal(code) {
  return (err) => {
    assert.ok(err instanceof DigestError, `expected a DigestError, got ${err}`);
    assert.equal(err.code, code, `${err.message}`);
    return true;
  };
}

// --- the severity table, exhaustive over the three triggers ---------------

for (const killed of [false, true]) {
  for (const failed of [false, true]) {
    for (const fast of [false, true]) {
      const want = killed ? SEVERITY.kill
        : failed ? SEVERITY.failed
          : fast ? SEVERITY.fast_burn
            : SEVERITY.info;

      test(`severity table: kill=${killed} failed=${failed} fast=${fast} -> ${want}`, () => {
        const d = doc({
          total: total({ kill: killed ? kill() : null, burn: burn({ fast }) }),
          finished: [finished({ status: failed ? 'failed' : 'succeeded', failed })],
          runs: [run()],
        });
        const got = digest(d);
        assert.equal(got.severity, want);
        assert.equal(got.project, 'fugaro');
        // The overall severity is the most severe item's, and every item's
        // severity is at most it: a digest never colours more than a document
        // carries.
        assert.ok(got.items.length > 0);
        for (const item of got.items) {
          assert.ok(
            ['kill', 'failed', 'fast_burn', 'info'].includes(item.severity),
            `item ${item.kind} carries severity ${item.severity}`,
          );
        }
        assert.ok(got.items.some((i) => i.severity === want));
      });
    }
  }
}

test('each trigger counts in both places it can appear', () => {
  // A kill on a repository block, and a fast burn on one, are the same two
  // severities as the project-wide pair -- the document carries both.
  assert.equal(digest(doc({ repos: [repo({ kill: kill() })] })).severity, SEVERITY.kill);
  assert.equal(digest(doc({ repos: [repo({ burn: burn({ fast: true }) })] })).severity, SEVERITY.fast_burn);
  // A failure is a finished *or* a ready entry: `isFailure` in
  // `internal/watch/finished.go` is the CLI's own rule, and the digest follows
  // it -- `failed`, `halted` and `infra_error` fail; `cancelled` does not.
  for (const status of ['failed', 'halted', 'infra_error']) {
    assert.equal(digest(doc({ finished: [finished({ status, failed: true })] })).severity, SEVERITY.failed, status);
  }
  assert.equal(digest(doc({ ready: [finished({ status: 'cancelled', failed: false })] })).severity, SEVERITY.info);
});

test('nothing else interrupts: a busy document with no trigger is info', () => {
  const got = digest(doc({
    runs: [run(), run({ run: 'r2', health: 'silent' }), run({ run: 'r3', health: 'lost', deadline: 'over' })],
    finished: [finished(), finished({ run: 'r9', status: 'cancelled' })],
  }));
  assert.equal(got.severity, SEVERITY.info);
  assert.ok(got.items.every((i) => i.severity === SEVERITY.info));
  assert.ok(got.items.some((i) => i.kind === 'run'));
});

test('an empty document is a digest of nothing, and it is info', () => {
  const got = digest(doc());
  assert.equal(got.severity, SEVERITY.info);
  assert.deepEqual(got.items, []);
});

// --- an absent field is `unavailable`, and never 0 ------------------------

test('a missing figure is unavailable, and never 0', () => {
  // A live run whose bucket row has no spend yet: `spent_usd` and
  // `age_seconds` are `*float64` on `JSONRun`, so they are absent, not zero.
  const live = run();
  delete live.spent_usd;
  delete live.age_seconds;

  const got = digest(doc({ runs: [live] }));
  const item = got.items.find((i) => i.kind === 'run');
  assert.equal(item.spent_usd, UNAVAILABLE);
  assert.equal(item.age_seconds, UNAVAILABLE);
  assert.notEqual(item.spent_usd, 0);
  assert.notEqual(item.age_seconds, 0);
  assert.equal(UNAVAILABLE, 'unavailable');
});

test('a field the document really carries as 0 stays 0', () => {
  // The other half of the rule: refusing to *invent* a zero is not refusing a
  // zero the document carries. A run that has spent nothing says so.
  const got = digest(doc({ runs: [run({ spent_usd: 0, age_seconds: 0 })] }));
  const item = got.items.find((i) => i.kind === 'run');
  assert.equal(item.spent_usd, 0);
  assert.equal(item.age_seconds, 0);

  const total_ = digest(doc({ total: total({ counted_usd: 0 }) }));
  assert.equal(total_.total.counted_usd, 0);
});

test('null is unavailable, exactly as absence is', () => {
  // `JSONTotal.DailyCapUSD` and `Percent` are pointers with no `omitempty`, so
  // the contract's own encoding of "no cap is configured" is `null`.
  const got = digest(doc({ total: total({ daily_cap_usd: null, percent: null }) }));
  assert.equal(got.total.daily_cap_usd, UNAVAILABLE);
  assert.equal(got.total.percent, UNAVAILABLE);
  assert.notEqual(got.total.daily_cap_usd, 0);
});

test('`leased_usd` is accepted on total and repo, and produces nothing', () => {
  // `leased_usd` is on `watch --json`'s `total` object and on every `repos[]`
  // entry now, written by the CLI. The digest accepts it -- a document
  // carrying it is not an unknown field -- but it reads nothing from it: no
  // item and no field on the summary, the same restraint the console's schema
  // keeps until a screen draws it.
  const withLease = digest(doc({
    total: total({ leased_usd: 2.5 }),
    repos: [repo({ leased_usd: 1.25 })],
  }));
  assert.deepEqual(withLease, digest(doc()));
  assert.equal(Object.hasOwn(withLease.total, 'leased_usd'), false);
  // A `null` is the contract's own "no value", so it is accepted too.
  assert.doesNotThrow(() => digest(doc({ total: total({ leased_usd: null }) })));

  // And the key is still fail-closed about what it carries: a string, or a
  // negative, is refused like every other NUMBER in these tables.
  assert.throws(() => digest(doc({ total: total({ leased_usd: '2.5' }) })), refusal(ERROR.TYPE));
  assert.throws(() => digest(doc({ repos: [repo({ leased_usd: -1 })] })), refusal(ERROR.RANGE));
});

test('`max_parallel` is accepted on total alone, and produces nothing', () => {
  // `max_parallel` -- the concurrent-run cap a launch is checked against -- is
  // on `watch --json`'s `total` object now, written by the CLI. The digest
  // accepts it -- a document carrying it is not an unknown field -- and reads
  // nothing from it, as it reads nothing from `leased_usd`.
  const withCap = digest(doc({ total: total({ max_parallel: 4 }) }));
  assert.deepEqual(withCap, digest(doc()));
  assert.equal(Object.hasOwn(withCap.total, 'max_parallel'), false);
  // A `null` is the contract's own "no cap could be read", so it is accepted.
  assert.doesNotThrow(() => digest(doc({ total: total({ max_parallel: null }) })));

  // It is a total's key and not a repository's: a cap bounds the project's
  // concurrent launches, so a `repos[]` entry carrying it is a document no CLI
  // writes and stays an unknown field.
  assert.throws(() => digest(doc({ repos: [repo({ max_parallel: 4 })] })), refusal(ERROR.UNKNOWN_FIELD));

  // And the key is fail-closed about what it carries, like every other NUMBER
  // in these tables.
  assert.throws(() => digest(doc({ total: total({ max_parallel: '4' }) })), refusal(ERROR.TYPE));
  assert.throws(() => digest(doc({ total: total({ max_parallel: -1 }) })), refusal(ERROR.RANGE));
});

test('a kill with no reason says unavailable for it, not an empty alarm', () => {
  const got = digest(doc({ total: total({ kill: kill({ reason: undefined, by: undefined }) }) }));
  const item = got.items.find((i) => i.kind === 'kill');
  assert.equal(item.severity, SEVERITY.kill);
  assert.equal(item.reason, UNAVAILABLE);
  assert.equal(item.by, UNAVAILABLE);
  assert.equal(item.at, '2026-10-17T17:00:00Z');
});

// --- fail closed ----------------------------------------------------------

test('an unknown field is refused, at every level the digest reads', () => {
  const killers = [
    [doc({ project_note: 'hello' }), ERROR.UNKNOWN_FIELD],
    [doc({ total: { ...total(), cap_left: 3 } }), ERROR.UNKNOWN_FIELD],
    [doc({ total: { ...total(), burn: { ...burn(), usd_per_minute: 1 } } }), ERROR.UNKNOWN_FIELD],
    [doc({ total: { ...total(), kill: { ...kill(), when: 'now' } } }), ERROR.UNKNOWN_FIELD],
    [doc({ repos: [{ ...repo(), pr: 'x' }] }), ERROR.UNKNOWN_FIELD],
    [doc({ runs: [{ ...run(), spend: 1 }] }), ERROR.UNKNOWN_FIELD],
    [doc({ finished: [{ ...finished(), sha: 'abc' }] }), ERROR.UNKNOWN_FIELD],
    [doc({ connection: { state: 'live', age_seconds: 1, since: 'x' } }), ERROR.UNKNOWN_FIELD],
  ];
  for (const [d, code] of killers) {
    assert.throws(() => digest(d), refusal(code));
  }
});

test('a wrong type is refused, never coerced', () => {
  const killers = [
    [doc({ project: 7 }), ERROR.TYPE],
    [doc({ runs: run() }), ERROR.TYPE], // a list where an object belongs
    [doc({ runs: [run({ spent_usd: '1.20' })] }), ERROR.TYPE],
    [doc({ runs: [run({ notional: 'no' })] }), ERROR.TYPE],
    [doc({ total: total({ counted_usd: '1' }) }), ERROR.TYPE],
    [doc({ total: total({ kill: 'by the operator' }) }), ERROR.TYPE],
    [doc({ finished: [finished({ failed: 'yes' })] }), ERROR.TYPE],
    [doc({ finished: [finished({ pr_number: '495' })] }), ERROR.TYPE],
  ];
  for (const [d, code] of killers) {
    assert.throws(() => digest(d), refusal(code));
  }
});

test('an oversize string field is refused, not truncated', () => {
  const huge = 'x'.repeat(4097);
  assert.throws(() => digest(doc({ runs: [run({ title: huge })] })), refusal(ERROR.OVERSIZE));
  // A field at the bound is fine: the bound refuses, it does not trim.
  assert.equal(digest(doc({ runs: [run({ title: 'x'.repeat(4096) })] })).severity, SEVERITY.info);
});

test("a value outside the contract's enumeration is refused", () => {
  assert.throws(() => digest(doc({ runs: [run({ health: 'gone' })] })), refusal(ERROR.ENUM_UNKNOWN));
  assert.throws(() => digest(doc({ runs: [run({ deadline: 'soon' })] })), refusal(ERROR.ENUM_UNKNOWN));
  assert.throws(() => digest(doc({ finished: [finished({ status: 'done' })] })), refusal(ERROR.ENUM_UNKNOWN));
});

test('a finished entry the digest cannot classify is refused, not guessed', () => {
  // Neither `failed` nor `status`: the digest does not get to assume success.
  const d = finished();
  delete d.failed;
  delete d.status;
  assert.throws(() => digest(doc({ finished: [d] })), refusal(ERROR.UNCLASSIFIED));
});

test('a document that contradicts itself is refused', () => {
  // `failed` is derived from `status` by the CLI's own `isFailure`, so a
  // document carrying both and disagreeing is not a document to act on.
  assert.throws(
    () => digest(doc({ finished: [finished({ status: 'succeeded', failed: true })] })),
    refusal(ERROR.CONTRADICTION),
  );
});

test('a list element that is not an object is refused with a code, not a TypeError', () => {
  // Every list the digest walks refuses the same input the same way: reading
  // a field out of the element before its shape is checked would raise a raw
  // TypeError, which is a crash rather than the refusal the plan asks for.
  const offenders = [
    [doc({ repos: [null] }), '$.repos[0]'],
    [doc({ finished: [null] }), '$.finished[0]'],
    [doc({ ready: [42] }), '$.ready[0]'],
    [doc({ runs: [null] }), '$.runs[0]'],
    [doc({ runs: ['a run'] }), '$.runs[0]'],
    [doc({ finished: [[]] }), '$.finished[0]'],
  ];
  for (const [d, path] of offenders) {
    assert.throws(() => digest(d), (err) => {
      assert.ok(err instanceof DigestError, `expected a DigestError, got ${err}`);
      assert.equal(err.code, ERROR.TYPE);
      assert.equal(err.path, path);
      return true;
    }, `${path} should be refused`);
  }
});

test('a document that is not a document is refused', () => {
  for (const bad of [null, undefined, 42, 'doc', [], true]) {
    assert.throws(() => digest(bad), refusal(ERROR.NOT_A_DOCUMENT));
  }
});

// --- the item order, and the ready/finished overlap -----------------------

test('the item order is the documented one, over all five groups at once', () => {
  // A consumer relies on this order (the README states it, and the interrupt
  // policy above this module reads the head of the list), so it is asserted
  // rather than left to whatever the walk happens to produce. The five groups:
  // the project's own triggers, each repository's, the ready finishes, the
  // other finishes, then the live runs.
  const got = digest(doc({
    total: total({ kill: kill(), burn: burn({ fast: true }) }),
    repos: [repo({ kill: kill(), burn: burn({ fast: true }) })],
    ready: [finished({ run: 'ready-1', outcome: 'ready' })],
    finished: [finished({ run: 'done-1' }), finished({ run: 'failed-1', status: 'failed', failed: true })],
    runs: [run(), run({ run: 'r2' })],
  }));
  assert.deepEqual(
    got.items.map((item) => [item.kind, item.run === undefined ? item.scope : item.run]),
    [
      ['kill', 'project'],
      ['fast_burn', 'project'],
      ['kill', 'repo'],
      ['fast_burn', 'repo'],
      ['ready', 'ready-1'],
      ['finished', 'done-1'],
      ['failed_finish', 'failed-1'],
      ['run', 'r1'],
      ['run', 'r2'],
    ],
  );
  assert.equal(got.severity, SEVERITY.kill);
});

test('a run in both ready and finished is emitted once, from ready', () => {
  // `ready` is a subset of `finished` in the contract
  // (`internal/watch/filter.go` derives it: the finished runs whose outcome is
  // ready), so the same run arrives twice in one document. A speaking
  // consumer must not say it twice, and a counting one must not count it
  // twice.
  const both = finished({ run: 'ready-1', outcome: 'ready' });
  const got = digest(doc({ ready: [both], finished: [both, finished({ run: 'done-1' })] }));
  assert.deepEqual(
    got.items.map((item) => [item.kind, item.run]),
    [['ready', 'ready-1'], ['finished', 'done-1']],
  );
  // Within one list the same rule applies to a repeated run, and the first
  // occurrence wins.
  const twice = digest(doc({ finished: [finished({ run: 'x' }), finished({ run: 'x' })] }));
  assert.equal(twice.items.length, 1);
  assert.equal(twice.items[0].run, 'x');
  // An entry with no run to identify is never deduped against another: only
  // an identified run is deduped.
  const anonymous = digest(doc({ finished: [finished({ run: undefined }), finished({ run: undefined })] }));
  assert.equal(anonymous.items.length, 2);
  assert.equal(anonymous.items[0].run, UNAVAILABLE);
});

test('a failed run is still a failure wherever it appears in the overlap', () => {
  // The dedupe must not swallow a failure: the copy that survives carries the
  // same fields and the same classification either way.
  const failedReady = digest(doc({ ready: [finished({ run: 'r', status: 'failed', failed: true })] }));
  assert.equal(failedReady.severity, SEVERITY.failed);
  assert.deepEqual(failedReady.items.map((i) => i.kind), ['failed_finish']);
});

// --- purity ---------------------------------------------------------------

test('the digest is a pure function of its argument: no clock, no randomness', () => {
  const d = doc({ total: total({ kill: kill() }), runs: [run()], finished: [finished()] });
  assert.deepEqual(digest(d), digest(d));
  assert.deepEqual(d, doc({ total: total({ kill: kill() }), runs: [run()], finished: [finished()] }));
});

// --- the independent review's findings (fix round) -------------------------

test('the overlap keeps the MORE SEVERE entry for a run, in either order', () => {
  const ok = finished({ run: 'a', status: 'succeeded', failed: false });
  const bad = finished({ run: 'a', status: 'failed', failed: true });
  for (const d of [doc({ ready: [ok], finished: [bad] }), doc({ ready: [bad], finished: [ok] })]) {
    const got = digest(d);
    assert.equal(got.severity, SEVERITY.failed);
    assert.deepEqual(got.items.map((i) => [i.kind, i.run]), [['failed_finish', 'a']]);
  }
  // Equal severity: the first still wins, so the documented order holds.
  const twin = digest(doc({ ready: [ok], finished: [finished({ run: 'a', title: 'later' })] }));
  assert.equal(twin.items.length, 1);
  assert.equal(twin.items[0].kind, 'ready');
});

test('a prototype key is an unknown field, never a known one', () => {
  for (const key of ['constructor', 'toString', 'hasOwnProperty', '__proto__', 'valueOf']) {
    const own = (extra) => JSON.parse(JSON.stringify(extra).replace('"KEY"', JSON.stringify(key)));
    const docs = [
      own({ KEY: 1, ...doc() }),
      own(doc({ runs: [{ ...run(), KEY: 1 }] })),
      own(doc({ total: { ...total(), burn: { ...burn(), KEY: 1 } } })),
    ];
    for (const d of docs) {
      assert.throws(() => digest(d), refusal(ERROR.UNKNOWN_FIELD), key);
    }
  }
  const viaInherit = Object.create({ project: 'inherited' });
  assert.equal(digest(viaInherit).project, UNAVAILABLE);
});

test('a repo burn is shape-checked whether or not it is fast', () => {
  assert.throws(
    () => digest(doc({ repos: [repo({ burn: { zzz: 1, fast: false } })] })),
    refusal(ERROR.UNKNOWN_FIELD),
  );
  assert.throws(() => digest(doc({ repos: [repo({ burn: [] })] })), refusal(ERROR.TYPE));
  assert.throws(() => digest(doc({ repos: [repo({ burn: { fast: 'no' } })] })), refusal(ERROR.TYPE));
  // And the project's burn behaves the same way, with no fast flag.
  assert.throws(() => digest(doc({ total: total({ burn: { zzz: 1 } }) })), refusal(ERROR.UNKNOWN_FIELD));
});

test('a burn with no fast flag raises no fast-burn item', () => {
  const got = digest(doc({
    total: total({ burn: { known: true, usd_per_min: 1 } }),
    repos: [repo({ burn: { known: true, usd_per_min: 1 } })],
  }));
  assert.equal(got.severity, SEVERITY.info);
  assert.deepEqual(got.items, []);
  assert.equal(got.total.burn.fast, UNAVAILABLE);
});

test('a number must be finite, and a count, age or amount must not be negative', () => {
  const bad = [NaN, Infinity, -Infinity, -1, -0.01];
  for (const v of bad) {
    const killers = [
      doc({ runs: [run({ spent_usd: v })] }),
      doc({ runs: [run({ age_seconds: v })] }),
      doc({ finished: [finished({ age_seconds: v })] }),
      doc({ finished: [finished({ pr_number: v })] }),
      doc({ total: total({ spent_usd: v }) }),
      doc({ total: total({ runs: v }) }),
      doc({ total: total({ burn: burn({ usd_per_min: v }) }) }),
      doc({ repos: [repo({ counted_usd: v })] }),
      doc({ connection: { state: 'live', age_seconds: v } }),
    ];
    for (const d of killers) {
      assert.throws(() => digest(d), refusal(ERROR.RANGE), String(v));
    }
  }
  assert.equal(digest(doc({ runs: [run({ spent_usd: 0, age_seconds: 0 })] })).items[0].spent_usd, 0);
});

test('a burn with known:false has an unavailable rate, never 0', () => {
  const got = digest(doc({
    total: total({ burn: burn({ known: false, usd_per_min: 0, fast: true }) }),
    repos: [repo({ burn: burn({ known: false, usd_per_min: 0, fast: true }) })],
  }));
  assert.equal(got.total.burn.usd_per_min, UNAVAILABLE);
  assert.equal(got.total.burn.known, false);
  const burns = got.items.filter((i) => i.kind === 'fast_burn');
  assert.equal(burns.length, 2);
  for (const item of burns) {
    assert.equal(item.usd_per_min, UNAVAILABLE);
  }
  // A known rate of 0 is a figure and stays one.
  const known = digest(doc({ total: total({ burn: burn({ known: true, usd_per_min: 0 }) }) }));
  assert.equal(known.total.burn.usd_per_min, 0);
});

test('each field is read exactly once: a getter cannot pass a check and then change', () => {
  const reads = {};
  const trap = (object, field, first, second) => {
    reads[field] = 0;
    Object.defineProperty(object, field, {
      enumerable: true,
      get() {
        reads[field] += 1;
        return reads[field] === 1 ? first : second;
      },
    });
    return object;
  };
  const sneaky = trap(run(), 'title', 'short', 'x'.repeat(100000));
  const got = digest(doc({ runs: [sneaky] }));
  assert.equal(got.items[0].title, 'short');
  assert.equal(reads.title, 1);

  const health = trap(run(), 'health', 'ok', 'gone');
  assert.equal(digest(doc({ runs: [health] })).items[0].health, 'ok');
  assert.equal(reads.health, 1);

  const spent = trap(run(), 'spent_usd', 1, -5);
  assert.equal(digest(doc({ runs: [spent] })).items[0].spent_usd, 1);
  assert.equal(reads.spent_usd, 1);

  const nested = trap(doc(), 'total', total(), 'not an object');
  assert.equal(digest(nested).total.spent_usd, 1);
  assert.equal(reads.total, 1);

  const burnObject = trap(burn({ fast: true }), 'fast', true, false);
  const viaBurn = digest(doc({ repos: [repo({ burn: burnObject })] }));
  assert.equal(viaBurn.severity, SEVERITY.fast_burn);
  assert.equal(reads.fast, 1);
});

test('a refusal never echoes the refused key or value', () => {
  const secretKey = 'sk-test-leaky-key-name';
  let seen;
  assert.throws(
    () => digest(doc({ runs: [{ ...run(), [secretKey]: 'v' }] })),
    (err) => {
      seen = err;
      return err.code === ERROR.UNKNOWN_FIELD;
    },
  );
  assert.ok(!seen.message.includes(secretKey));
  assert.ok(!seen.path.includes(secretKey));
  assert.equal(seen.path, '$.runs[0]');
  assert.throws(
    () => digest(doc({ runs: [run({ health: 'sk-test-leaky-value' })] })),
    (err) => !err.message.includes('leaky') && !err.path.includes('leaky'),
  );
});

test('the oversize bound is in UTF-8 bytes, not in characters', () => {
  // 2049 two-byte characters: 2049 code units, 4098 bytes.
  assert.throws(
    () => digest(doc({ runs: [run({ title: 'é'.repeat(2049) })] })),
    refusal(ERROR.OVERSIZE),
  );
  assert.equal(
    digest(doc({ runs: [run({ title: 'é'.repeat(2048) })] })).items[0].title.length,
    2048,
  );
});

test('an enumeration value that is a prototype key is refused', () => {
  for (const key of ['constructor', 'toString', '__proto__', 'hasOwnProperty']) {
    assert.throws(() => digest(doc({ runs: [run({ health: key })] })), refusal(ERROR.ENUM_UNKNOWN), key);
    assert.throws(() => digest(doc({ runs: [run({ deadline: key })] })), refusal(ERROR.ENUM_UNKNOWN), key);
    assert.throws(
      () => digest(doc({ finished: [finished({ status: key, failed: undefined })] })),
      refusal(ERROR.ENUM_UNKNOWN),
      key,
    );
  }
});

test('a list with a hole is refused, not walked around', () => {
  // eslint-disable-next-line no-sparse-arrays
  const holey = (entry) => [entry, , entry];
  for (const d of [
    doc({ runs: holey(run()) }),
    doc({ repos: holey(repo()) }),
    doc({ finished: holey(finished({ run: 'a' })) }),
    doc({ ready: holey(finished({ run: 'a' })) }),
  ]) {
    assert.throws(() => digest(d), refusal(ERROR.TYPE));
  }
});
