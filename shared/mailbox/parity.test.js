// The mailbox state machine's parity corpus, read back and re-derived (mobile
// plan, Task P4.1 T8).
//
// `shared/mailbox` is the module the Go consumer binds, and the binding is held
// to it by **committed data** (ruling M9): `parity.generate.mjs` — a script,
// not a test, the convention `shared/mailbox-schema/vectors.generate.mjs` and
// `shared/lease/parity.generate.mjs` set — drives the module (and, for the
// seeded half, `shared/mailbox-sim`) and writes the answers it gave to
// `internal/mailbox/testdata/parity.json`, which `internal/mailbox/parity_test.go`
// replays with no Node, no clock and no network. The plan names this file as
// the generator; the writer is the `.mjs` beside it, and the split is the
// reason this file exists at all:
//
//   * `npm test` cannot rewrite the committed corpus from a mutated module.
//     `shared/mailbox/mutation-proof.sh` applies a mutation to `index.js`, runs
//     this suite, and restores `index.js` and nothing else: a test that
//     regenerated the corpus would leave the mutated answers committed in the
//     working tree, and the Go suite red for a reason no one could see.
//   * The corpus is not trusted, it is **re-derived**: every scenario's answers
//     and the state it ends in are recomputed here by calling the live module,
//     so a corpus that has drifted from the module fails here — in the suite
//     that runs the mutation harness — rather than only in the Go replay.
//
// To change the corpus, change `parity.generate.mjs` and run
// `node shared/mailbox/parity.generate.mjs`; a stale corpus is a failing test,
// never a silent rewrite.

import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import {
  DEFAULT_FUTURE_TOLERANCE_MS,
  DEFAULT_MAX_SEEN,
  DEFAULT_WINDOW_MS,
  KIND,
  OUTCOME,
  REFUSAL,
  STATE,
  defaultMaxPerSender,
  step,
} from './index.js';
import { PROMPT_TTL_MS } from '../prompts/index.js';

// The corpus path is written out here rather than imported from the generator:
// importing that file would run it, and a test that runs the writer is exactly
// what this file exists not to be.
const CORPUS = new URL('../../internal/mailbox/testdata/parity.json', import.meta.url);

const REGENERATE = 'node shared/mailbox/parity.generate.mjs';

/** The five numbers a scenario is judged with: the corpus's, or its own. */
function optionsOf(corpus, scenario) {
  return scenario.options === undefined ? corpus.options : scenario.options;
}

/**
 * The phase the ledger says the mailbox is in. A deliberate second
 * implementation of the generator's — one shared copy would let a wrong
 * projection pass both ends — and the module's own refusal (`invalid-state`)
 * is what catches a wrong one, since it is handed the rebuilt state.
 */
function phaseOf(seen) {
  if (!Array.isArray(seen)) return STATE.IDLE;
  if (seen.filter((entry) => entry.status === 'accepted').length > 0) return STATE.PENDING;
  if (seen.filter((entry) => entry.status === 'acted').length > 0) return STATE.ACTED;
  return STATE.IDLE;
}

/** The module's state, rebuilt from the three fields the corpus stores. */
function stateOf(snapshot, options) {
  return {
    phase: phaseOf(snapshot.seen),
    now: snapshot.now,
    windowMs: options.windowMs,
    maxSeen: options.maxSeen,
    maxPerSender: options.maxPerSender,
    promptTtlMs: options.promptTtlMs,
    futureToleranceMs: options.futureToleranceMs,
    evictedThrough: snapshot.evictedThrough,
    seen: snapshot.seen,
  };
}

/** The corpus's own spelling of a state: the three fields, in the file's order. */
function snapshotOf(state) {
  return {
    now: state.now,
    evictedThrough: state.evictedThrough,
    seen: Array.isArray(state.seen)
      ? state.seen.map((entry) => ({
          sender: entry.sender,
          id: entry.id,
          at: entry.at,
          kind: entry.kind,
          status: entry.status,
        }))
      : state.seen,
  };
}

/**
 * One event, as the Go binding takes it: `perform` is `Ledger.Perform` — the
 * module's `deliver` and, for a delivery it takes, the `act` that follows it in
 * the same pass — and `ack` is `Ledger.Ack`. The answer is the decision: the
 * outcome and the pair it is about, or the refusal's code.
 */
function drive(state, event, options) {
  if (event.op === 'ack') {
    const result = step(state, { type: 'ack', now: event.now });
    if (!result.ok) return { state, answer: { code: result.code } };
    return { state: result.state, answer: { outcome: result.outcome, id: result.id, sender: result.sender } };
  }

  const delivered = step(state, {
    type: 'deliver',
    now: event.now,
    message: {
      id: event.message.id,
      sender: event.message.sender,
      text: event.message.text,
      at: event.message.at,
      // A valid tag, and any valid tag: the ledger reads the pair, the `at` and
      // the kind, and never a message's mac (T2's), which is why the corpus
      // does not carry it.
      mac: 'f'.repeat(64),
    },
    kind: event.kind,
  });
  if (!delivered.ok) return { state, answer: { code: delivered.code } };
  if (delivered.outcome !== OUTCOME.ACCEPTED) {
    return {
      state: delivered.state,
      answer: { outcome: delivered.outcome, id: delivered.id, sender: delivered.sender },
    };
  }
  const acted = step(delivered.state, { type: 'act', now: event.now });
  assert.equal(acted.ok, true, `${event.message.id}: the act after a delivery the module took was refused`);
  return { state: acted.state, answer: { outcome: acted.outcome, id: acted.id, sender: acted.sender } };
}

/** The committed corpus, decoded. There is no fallback: absent is a failure. */
function corpus() {
  let raw;
  try {
    raw = readFileSync(CORPUS, 'utf8');
  } catch {
    assert.fail(
      `the mailbox parity corpus is missing (internal/mailbox/testdata/parity.json); ` +
        `generate it with \`${REGENERATE}\` and commit it`,
    );
  }
  return JSON.parse(raw);
}

test('the corpus is the module’s current answers, scenario by scenario', () => {
  const built = corpus();
  assert.equal(built.module, 'shared/mailbox/index.js');
  assert.equal(built.replay, 'internal/mailbox/parity_test.go');
  assert.ok(built.scenarios.length >= 150, `the corpus carries ${built.scenarios.length} scenarios; it has been truncated`);

  for (const scenario of built.scenarios) {
    const options = optionsOf(built, scenario);
    let state = stateOf(scenario.state, options);
    scenario.events.forEach((event, index) => {
      const driven = drive(state, event, options);
      state = driven.state;
      const recorded = { ...event };
      delete recorded.op;
      delete recorded.now;
      delete recorded.kind;
      delete recorded.message;
      assert.deepEqual(
        driven.answer,
        recorded,
        `${scenario.name}: event ${index} is no longer what the module answers — ` +
          `regenerate with \`${REGENERATE}\` if the change is intended`,
      );
    });
    if (scenario.stateRefusal !== undefined) continue;
    assert.deepEqual(
      snapshotOf(state),
      scenario.final,
      `${scenario.name}: the state the module produces has drifted from the corpus`,
    );
  }
});

test('the reader cases are the states the module refuses, by their own code', () => {
  const built = corpus();
  const read = built.scenarios.filter((scenario) => scenario.stateRefusal !== undefined);
  assert.ok(read.length >= 8, `the corpus carries ${read.length} reader cases`);
  for (const scenario of read) {
    assert.deepEqual(scenario.events, [], `${scenario.name}: a state that cannot be read has no events`);
    const result = step(stateOf(scenario.state, optionsOf(built, scenario)), {
      type: 'ack',
      now: scenario.state.now,
    });
    assert.equal(result.ok, false, `${scenario.name}: the module read a state the corpus says it refuses`);
    assert.equal(result.code, scenario.stateRefusal, `${scenario.name}: the module refuses it with another code`);
  }
});

test('the corpus carries the module’s vocabulary and its numbers, not a copy of them', () => {
  const built = corpus();
  assert.deepEqual(built.outcomes, Object.values(OUTCOME));
  assert.deepEqual(Object.keys(built.codes).sort(), Object.keys(REFUSAL).sort());
  for (const [name, code] of Object.entries(REFUSAL)) assert.equal(built.codes[name], code);
  assert.deepEqual(built.options, {
    windowMs: DEFAULT_WINDOW_MS,
    promptTtlMs: PROMPT_TTL_MS,
    maxSeen: DEFAULT_MAX_SEEN,
    maxPerSender: defaultMaxPerSender(DEFAULT_MAX_SEEN),
    futureToleranceMs: DEFAULT_FUTURE_TOLERANCE_MS,
  });
});

test('the codes the corpus does not carry are the ones this half cannot ask for', () => {
  const carried = new Set();
  for (const scenario of corpus().scenarios) {
    if (scenario.stateRefusal !== undefined) carried.add(scenario.stateRefusal);
    for (const event of scenario.events) if (event.code !== undefined) carried.add(event.code);
  }
  // The 16 the Go binding can be answered with: the reader's six, the
  // delivery's seven, the two `now` guards and the kind.
  for (const name of [
    'INVALID_NOW',
    'INVALID_EVICTED_THROUGH',
    'INVALID_SEEN',
    'INVALID_SEEN_ENTRY',
    'DUPLICATE_SEEN_ENTRY',
    'UNSORTED_SEEN',
    'NOW_WENT_BACKWARDS',
    'NOW_JUMPED',
    'UNKNOWN_KIND',
    'ZERO_TIMESTAMP',
    'REPLAY',
    'EXPIRED',
    'FUTURE_TIMESTAMP',
    'ID_CONFLICT',
    'DEDUPE_FULL',
    'SENDER_FULL',
  ]) {
    assert.ok(carried.has(REFUSAL[name]), `no case carries REFUSAL.${name} (${REFUSAL[name]})`);
  }
  // ... and the rest are named rather than left as a silent gap: a code that
  // cannot be asked for is a decision, and this is where it is written down.
  for (const name of [
    // The schema's, raised on a value that is not a message: the gate refuses
    // one before the ledger sees it, and the schema's own vectors (T1/T2) are
    // where they are replayed.
    'NOT_AN_OBJECT',
    'UNKNOWN_FIELD',
    'MISSING_FIELD',
    // A Proxy or a throwing getter: not expressible in JSON.
    'ACCESSOR_PROPERTY',
    'INTERNAL_ERROR',
    // The corpus stores the ledger's three fields and both sides derive the
    // phase, so a state whose phase disagrees with its ledger — the one shape
    // this refusal is about — cannot be written down here.
    'UNKNOWN_STATE',
    'INVALID_STATE',
    // The options. The Go side refuses a window, a cap or a TTL outside the
    // module's bounds with an ordinary error and not with a code, and the
    // corpus's own numbers are always the module's.
    'INVALID_WINDOW',
    'INVALID_MAX_SEEN',
    'INVALID_MAX_PER_SENDER',
    'INVALID_PROMPT_TTL',
    'INVALID_FUTURE_TOLERANCE',
    // JSON has no negative zero.
    'NEGATIVE_ZERO',
    // The event vocabulary: the corpus's events are the binding's two calls.
    'UNKNOWN_EVENT',
    // Nothing in this half closes a mailbox.
    'MAILBOX_CLOSED',
  ]) {
    assert.equal(carried.has(REFUSAL[name]), false, `a case carries REFUSAL.${name}, and none should`);
  }
});

test('the seeded half is the sweep the plan’s verification names', () => {
  const built = corpus();
  const seeded = built.scenarios.filter((scenario) => scenario.seed !== null);
  assert.equal(seeded.length, 120);
  const seeds = new Set(seeded.map((scenario) => scenario.seed));
  assert.equal(seeds.size, 120, 'the corpus repeats a seed');
  for (const scenario of seeded) {
    // The schedule is the simulator's own text, and it is what a failing case
    // prints: a scenario that lost it cannot be replayed without the corpus.
    const schedule = JSON.parse(scenario.scheduleText);
    assert.equal(schedule.seed, scenario.seed, `${scenario.name}: the schedule is another run's`);
    assert.ok(Array.isArray(schedule.faults), `${scenario.name}: the schedule carries no faults`);
    // Three messages, two consumers and twelve passes: the messages the phone
    // wrote are the ids every delivery in the scenario names.
    const written = new Set(scenario.events.map((event) => event.message?.id).filter(Boolean));
    assert.ok(written.size <= 3, `${scenario.name}: ${written.size} messages, and the sweep writes three`);
    assert.ok(scenario.events.length >= 3, `${scenario.name}: too few events to be a sweep`);
  }
  for (const scenario of built.scenarios.filter((entry) => entry.seed === null)) {
    assert.equal(scenario.scheduleText, null, `${scenario.name}: a hand-built case carries no schedule`);
  }
});

test('the corpus’s hash covers every other byte of it', () => {
  const raw = readFileSync(CORPUS, 'utf8');
  const recorded = JSON.parse(raw).hash;
  assert.match(recorded, /^[0-9a-f]{64}$/);
  const zeroed = raw.replace(recorded, '0'.repeat(64));
  const computed = createHash('sha256').update(zeroed, 'utf8').digest('hex');
  assert.equal(
    computed,
    recorded,
    'the corpus has been edited by hand: the hash inside it is over the bytes it does not match — ' +
      `regenerate with \`${REGENERATE}\` rather than editing it`,
  );
});

test('no event judged under a kind the module does not define is ever taken', () => {
  const built = corpus();
  const defined = new Set(Object.values(KIND));
  const seen = new Set();
  let asked = false;
  for (const scenario of built.scenarios) {
    for (const event of scenario.events) {
      if (event.kind === undefined) continue; // an ack is judged under no kind
      if (defined.has(event.kind)) {
        seen.add(event.kind);
        continue;
      }
      if (event.code === REFUSAL.UNKNOWN_KIND) {
        asked = true;
        continue;
      }
      assert.ok(
        event.code !== undefined,
        `${scenario.name}: ${JSON.stringify(event.kind)} is not a kind and the event was not refused`,
      );
    }
  }
  assert.ok(asked, 'no case asks for the unknown-kind refusal');
  for (const kind of Object.values(KIND)) {
    assert.ok(seen.has(kind), `no event is judged as a ${kind}`);
  }
});
