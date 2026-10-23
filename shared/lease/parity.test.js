// The consumer lease's parity corpus, read back and re-derived (mobile plan,
// Task P4.1 T3).
//
// `shared/lease` is the module the Go consumer binds, and the binding is held
// to it by **committed data** (ruling M9): `parity.generate.mjs` — a script,
// not a test, the convention `shared/mailbox-schema/vectors.generate.mjs` set —
// writes the module's own answers for the inputs that decide who may act on a
// mailbox to `internal/mailbox/testdata/lease-parity.json`, which
// `internal/mailbox/lease_test.go` replays with no Node, no clock and no
// network.
//
// **This file reads that corpus and writes nothing.** Two things follow, and
// both are why it is shaped this way:
//
//   * `npm test` cannot rewrite the committed corpus from a mutated module.
//     `shared/lease/mutation-proof.sh` applies a mutation to `index.js`, runs
//     this suite, and restores `index.js` and nothing else: a test that
//     regenerated the corpus would leave the mutated answers committed in the
//     working tree, and the Go suite red for a reason no one could see.
//   * The corpus is not trusted, it is **re-derived**: every case's `expect`
//     is recomputed here by calling the live module, so a case whose recorded
//     answer has drifted from the module fails here — in the suite that runs
//     the mutation harness — rather than only in the Go replay.
//
// To change the corpus, change `parity.generate.mjs` and run
// `node shared/lease/parity.generate.mjs`; a stale corpus is a failing test,
// never a silent rewrite.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import {
  CLAIM_TTL,
  LEASE_CODES,
  MAX_CLOCK_SKEW,
  MAX_HOLDER_LENGTH,
  MAX_TIMESTAMP,
  MAX_TTL,
  readLease,
  releaseLease,
  takeLease,
} from './index.js';
// The corpus path is written out here rather than imported from the generator:
// importing that file would run it, and a test that runs the writer is exactly
// what this file exists not to be.
const CORPUS = new URL('../../internal/mailbox/testdata/lease-parity.json', import.meta.url);

const REGENERATE = 'node shared/lease/parity.generate.mjs';

// The projection the corpus records: the DECISION and never the wording. It is
// a deliberate second implementation of the generator's — one shared copy would
// let a wrong projection pass both ends — and it is what makes the re-derivation
// below a check rather than a tautology.
function answer(result) {
  const out = { ok: result.ok };
  if (!result.ok) {
    out.code = result.code;
    if (result.because !== undefined) out.because = result.because;
  }
  if ('lease' in result) out.lease = result.lease === null ? null : leaseOf(result.lease);
  if ('existing' in result) out.existing = leaseOf(result.existing);
  if ('released' in result) out.released = leaseOf(result.released);
  if ('state' in result) out.state = stateOf(result.state);
  return out;
}

function leaseOf(value) {
  return { holder: value.holder, at: value.at };
}

function stateOf(value) {
  return { claim: value.claim === null ? null : leaseOf(value.claim), ttl: value.ttl };
}

/** The committed corpus, decoded. There is no fallback: absent is a failure. */
function corpus() {
  let raw;
  try {
    raw = readFileSync(CORPUS, 'utf8');
  } catch {
    assert.fail(
      `the lease parity corpus is missing (internal/mailbox/testdata/lease-parity.json); ` +
        `generate it with \`${REGENERATE}\` and commit it`,
    );
  }
  return JSON.parse(raw);
}

test('the corpus is the module’s current answers, case by case', () => {
  const { module, cases } = corpus();
  assert.equal(module, 'shared/lease/index.js');
  assert.ok(cases.length >= 30, `the corpus carries ${cases.length} cases; it has been truncated`);

  const calls = { read: readLease, take: takeLease, release: releaseLease };
  for (const c of cases) {
    const call = calls[c.op];
    assert.ok(call, `${c.name}: the corpus records the operation ${c.op}`);
    const result = call(c.state, c.request);
    assert.deepEqual(
      answer(result),
      c.expect,
      `${c.name}: the module no longer answers what the committed corpus records — ` +
        `regenerate with \`${REGENERATE}\` if the change is intended`,
    );
  }
});

test('the corpus carries the module’s constants, not a copy of them', () => {
  const { constants, codes } = corpus();
  assert.deepEqual(constants, {
    CLAIM_TTL,
    MAX_CLOCK_SKEW,
    MAX_TTL,
    MAX_TIMESTAMP,
    MAX_HOLDER_LENGTH,
  });
  assert.deepEqual(Object.keys(codes).sort(), Object.keys(LEASE_CODES).sort());
});

test('every refusal code the module declares is carried by a case', () => {
  const carried = new Set();
  for (const c of corpus().cases) {
    if (c.expect.ok === false) carried.add(c.expect.code);
    if (c.expect.because !== undefined) carried.add(c.expect.because);
  }
  for (const [name, code] of Object.entries(LEASE_CODES)) {
    // `already-claimed`, `not-holder` and `no-lease` are outcomes rather than
    // malformed values, and they are carried too: a code the corpus stops
    // carrying is a decision the Go replay has stopped comparing.
    assert.ok(carried.has(code), `no case carries LEASE_CODES.${name} (${code})`);
  }
});

test('the corpus carries the boundaries the plan’s verification names', () => {
  const names = new Set(corpus().cases.map((c) => c.name));
  for (const name of [
    'a lease is fresh one millisecond before its TTL',
    'a lease is stale at exactly its TTL',
    'a lease exactly MAX_CLOCK_SKEW ahead is fresh',
    'a lease one millisecond past the skew is taken over',
    'a lease ahead to the ceiling cannot block a mailbox for ever',
    'a take on an unreadable state is refused, never taken over',
  ]) {
    assert.ok(names.has(name), `the corpus no longer carries ${JSON.stringify(name)}`);
  }
});
