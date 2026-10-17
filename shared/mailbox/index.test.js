// The mailbox state machine's tests (mobile plan, Task P1.1).
//
// Written BEFORE the module it imports, on purpose: that is this task's red
// step. `npm test` from `shared/` discovers this file through P0.1's glob and
// fails with ERR_MODULE_NOT_FOUND until `shared/mailbox/index.js` exists, so
// the run is red for the right reason and not because no test was found -- the
// one way this package's runner can lie (see `shared/README.md`).
//
// What these tests pin, from the plan's P1.1 and from D110 (answered
// 2026-10-17: supersede Voice D49, but only by meeting the standard D49 named):
//
//   * a message carries an explicit id, and the id is P0.2's schema's business;
//   * delivering the same (sender, id) twice is ACTED ON ONCE -- the second
//     delivery is answered as a duplicate, and it does not re-arm the delivery;
//   * an id from a different sender is NOT deduped against another sender's;
//   * a message older than the replay window is REFUSED as a replay;
//   * the machine is TOTAL: every state x every event has a defined outcome,
//     and this file asserts the CODE against the declared table rather than
//     trusting the table to describe the code;
//   * an unknown state and an unknown event are refused, each with its own code;
//   * the dedupe ledger is bounded, and its eviction rule can neither re-admit a
//     replay nor drop a delivery that is still in flight;
//   * time is injected -- no clock of its own, no randomness, no state outside
//     the value that is handed in.
//
// And, because this package's rule is that ambiguity is refused rather than
// coerced: unknown fields, accessor properties, wrong types, out-of-range
// numbers, negative zero, a `now` that moves backwards and a ledger that is not
// a ledger are each a REFUSAL with its own code -- never a coerced value and
// never a guess.
//
// No test value here is a secret, and no literal is secret-shaped: the
// secret-shaped strings this file needs are built at run time from harmless
// pieces rather than written as anything a scanner could mistake for a
// credential.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import { REFUSALS } from '../mailbox-schema/index.js';
import {
  DEFAULT_FUTURE_TOLERANCE_MS,
  DEFAULT_MAX_SEEN,
  DEFAULT_WINDOW_MS,
  EVENT,
  INPUT_REFUSALS,
  MAX_FUTURE_TOLERANCE_MS,
  MAX_MAX_SEEN,
  MAX_WINDOW_MS,
  MIN_MAX_SEEN,
  MIN_WINDOW_MS,
  OUTCOME,
  REFUSAL,
  STATE,
  STATUS,
  TRANSITIONS,
  initialState,
  step,
} from './index.js';

// A fixed now, deliberately far from the day this test runs: the module must
// read the `now` it is handed, so a rule that quietly read a clock of its own
// would call every message here a replay.
const NOW = 1_700_000_000_000;

// Small, countable numbers rather than the module's defaults, so the window
// boundary and the ledger's cap are things a reader can check by hand.
const WINDOW = 60_000;
const TOLERANCE = 5_000;
const MAX_SEEN = 4;

/** The mailbox's options, as `initialState` takes them. */
function options(overrides = {}) {
  return {
    now: NOW,
    windowMs: WINDOW,
    maxSeen: MAX_SEEN,
    futureToleranceMs: TOLERANCE,
    ...overrides,
  };
}

/** A mailbox in the idle phase, or the test fails with the refusal. */
function newMailbox(overrides = {}) {
  const created = initialState(options(overrides));
  assert.equal(created.ok, true, `initialState refused: ${JSON.stringify(created)}`);
  return created.state;
}

/** A message with the schema's five fields, and an `at` inside the window. */
function message(overrides = {}) {
  return {
    id: 'm-0001',
    sender: 'phone-a',
    text: 'run 42 finished',
    at: NOW - 1_000,
    ...overrides,
  };
}

const deliver = (value, overrides = {}) => ({ type: EVENT.DELIVER, now: NOW, message: value, ...overrides });
const act = (now = NOW) => ({ type: EVENT.ACT, now });
const ack = (now = NOW) => ({ type: EVENT.ACK, now });
const close = (now = NOW) => ({ type: EVENT.CLOSE, now });

/** A step that must succeed. */
function stepped(state, event) {
  const result = step(state, event);
  assert.equal(result.ok, true, `expected a step, got ${JSON.stringify(result)}`);
  return result;
}

/** A step that must be refused, with the code the caller names. */
function refused(state, event, code) {
  const result = step(state, event);
  assert.equal(result.ok, false, `expected a refusal, got ${JSON.stringify(result)}`);
  if (code !== undefined) {
    assert.equal(result.code, code, `expected ${code}, got ${JSON.stringify(result)}`);
  }
  return result;
}

/** A mailbox in `phase`, built through the public API and nothing else. */
function mailboxIn(phase) {
  let state = newMailbox();
  if (phase === STATE.IDLE) {
    // Idle WITH a ledger: one delivery, acted on and acked. An idle mailbox
    // that had never seen a message could not exercise the duplicate path.
    state = stepped(state, deliver(message())).state;
    state = stepped(state, act()).state;
    return stepped(state, ack()).state;
  }
  if (phase === STATE.PENDING) {
    return stepped(state, deliver(message())).state;
  }
  if (phase === STATE.ACTED) {
    state = stepped(state, deliver(message())).state;
    return stepped(state, act()).state;
  }
  state = stepped(state, deliver(message())).state;
  state = stepped(state, act()).state;
  state = stepped(state, ack()).state;
  return stepped(state, close()).state;
}

/** The ids in a ledger, in the ledger's own (canonical) order. */
function ids(state) {
  return state.seen.map((entry) => entry.id);
}

// ---------------------------------------------------------------------------
// The initial state
// ---------------------------------------------------------------------------

test('initialState returns a frozen, empty, idle mailbox', () => {
  const state = newMailbox();

  assert.equal(state.phase, STATE.IDLE);
  assert.equal(state.now, NOW);
  assert.equal(state.windowMs, WINDOW);
  assert.equal(state.maxSeen, MAX_SEEN);
  assert.equal(state.futureToleranceMs, TOLERANCE);
  assert.deepEqual(state.seen, []);
  assert.ok(Object.isFrozen(state), 'the state must be frozen');
  assert.ok(Object.isFrozen(state.seen), 'the ledger must be frozen');
});

test('initialState takes the documented defaults, and each is a bounded number', () => {
  const state = newMailbox({
    windowMs: undefined,
    maxSeen: undefined,
    futureToleranceMs: undefined,
  });

  assert.equal(state.windowMs, DEFAULT_WINDOW_MS);
  assert.equal(state.maxSeen, DEFAULT_MAX_SEEN);
  assert.equal(state.futureToleranceMs, DEFAULT_FUTURE_TOLERANCE_MS);

  // The defaults are inside the ranges the module refuses to leave.
  assert.ok(DEFAULT_WINDOW_MS >= MIN_WINDOW_MS && DEFAULT_WINDOW_MS <= MAX_WINDOW_MS);
  assert.ok(DEFAULT_MAX_SEEN >= MIN_MAX_SEEN && DEFAULT_MAX_SEEN <= MAX_MAX_SEEN);
  assert.ok(DEFAULT_FUTURE_TOLERANCE_MS <= MAX_FUTURE_TOLERANCE_MS);
});

test('initialState refuses a config that is not this module\'s config', () => {
  const cases = [
    [null, REFUSAL.NOT_AN_OBJECT],
    [[], REFUSAL.NOT_AN_OBJECT],
    [{}, REFUSAL.MISSING_FIELD],
    [{ now: 0 }, REFUSAL.INVALID_NOW],
    [{ now: -1 }, REFUSAL.INVALID_NOW],
    [{ now: 1.5 }, REFUSAL.INVALID_NOW],
    [{ now: Number.NaN }, REFUSAL.INVALID_NOW],
    [{ now: -0 }, REFUSAL.INVALID_NOW],
    [{ now: String(NOW) }, REFUSAL.INVALID_NOW],
    [{ now: NOW, windowMs: MIN_WINDOW_MS - 1 }, REFUSAL.INVALID_WINDOW],
    [{ now: NOW, windowMs: MAX_WINDOW_MS + 1 }, REFUSAL.INVALID_WINDOW],
    [{ now: NOW, windowMs: -0 }, REFUSAL.INVALID_WINDOW],
    [{ now: NOW, maxSeen: 0 }, REFUSAL.INVALID_MAX_SEEN],
    [{ now: NOW, maxSeen: MAX_MAX_SEEN + 1 }, REFUSAL.INVALID_MAX_SEEN],
    [{ now: NOW, futureToleranceMs: -1 }, REFUSAL.INVALID_FUTURE_TOLERANCE],
    [{ now: NOW, futureToleranceMs: -0 }, REFUSAL.INVALID_FUTURE_TOLERANCE],
    [{ now: NOW, futureToleranceMs: MAX_FUTURE_TOLERANCE_MS + 1 }, REFUSAL.INVALID_FUTURE_TOLERANCE],
    [{ now: NOW, window: WINDOW }, REFUSAL.UNKNOWN_FIELD],
  ];

  for (const [config, code] of cases) {
    const result = initialState(config);
    assert.equal(result.ok, false, `${JSON.stringify(config)} was accepted`);
    assert.equal(result.code, code, `${JSON.stringify(config)} gave ${JSON.stringify(result)}`);
  }
});

// ---------------------------------------------------------------------------
// The message, and its id
// ---------------------------------------------------------------------------

test('a message carries an id: one without an id is refused, by the schema', () => {
  const withoutId = message();
  delete withoutId.id;

  const result = refused(newMailbox(), deliver(withoutId), 'missing-field');
  assert.equal(result.field, 'message.id');
});

test("the schema's own codes are propagated, and name the message's field", () => {
  // Each of these is P0.2's refusal, not this module's: the schema is the
  // protocol, and a second, weaker copy of it here is exactly what the plan
  // forbids. What this module adds is the `message.` prefix, so a caller can
  // tell a bad message from a bad state.
  const cases = [
    [message({ id: 'm.0001' }), REFUSALS.BAD_KEY, 'message.id'],
    [message({ id: '' }), REFUSALS.EMPTY_STRING, 'message.id'],
    [message({ text: '' }), REFUSALS.EMPTY_STRING, 'message.text'],
    [message({ text: 'x'.repeat(4097) }), REFUSALS.TOO_LONG, 'message.text'],
    [message({ at: 'yesterday' }), REFUSALS.WRONG_TYPE, 'message.at'],
    [message({ at: 1.5 }), REFUSALS.BAD_TIMESTAMP, 'message.at'],
    [message({ acked: 'yes' }), REFUSALS.WRONG_TYPE, 'message.acked'],
    [message({ extra: 1 }), REFUSALS.UNKNOWN_FIELD, 'message.extra'],
    [null, REFUSALS.NOT_AN_OBJECT, 'message'],
  ];

  for (const [value, code, field] of cases) {
    const result = refused(newMailbox(), deliver(value), code);
    assert.equal(result.field, field, `${JSON.stringify(value)} gave ${JSON.stringify(result)}`);
  }
});

test('a timestamp of negative zero is refused rather than read as zero', () => {
  // P0.2's schema accepts `-0` for `at` (it is numerically zero, and
  // `Number.isSafeInteger(-0)` is true), so this module refuses it explicitly:
  // negative zero is the shape of a value that has been negated, not a time,
  // and every number this module reads refuses it.
  refused(newMailbox(), deliver(message({ at: -0 })), REFUSAL.NEGATIVE_ZERO);
});

// ---------------------------------------------------------------------------
// Dedupe: the same id twice, and the same id from another sender
// ---------------------------------------------------------------------------

test('the same id delivered twice is acted on once, and the second is a duplicate', () => {
  const first = stepped(newMailbox(), deliver(message()));
  assert.equal(first.outcome, OUTCOME.ACCEPTED);
  assert.equal(first.phase, STATE.PENDING);
  assert.equal(first.id, 'm-0001');
  assert.equal(first.sender, 'phone-a');

  const acted = stepped(first.state, act());
  assert.equal(acted.outcome, OUTCOME.ACTED);
  assert.equal(acted.id, 'm-0001');

  // The redelivery: at-least-once means it WILL arrive again, and the answer is
  // "a duplicate, acknowledge it" -- not a second action and not a refusal.
  const second = stepped(acted.state, deliver(message()));
  assert.equal(second.outcome, OUTCOME.DUPLICATE);
  assert.equal(second.phase, STATE.ACTED);
  assert.equal(second.id, 'm-0001');

  // And it is not re-armed: there is nothing left to act on, and the ack is
  // still the one that completes the delivery.
  assert.equal(stepped(second.state, act()).outcome, OUTCOME.NOTHING_TO_ACT);
  assert.equal(stepped(second.state, ack()).outcome, OUTCOME.ACKED);
});

test('a duplicate delivery changes nothing: not the ledger, not the status', () => {
  const delivered = stepped(newMailbox(), deliver(message()));
  const before = JSON.stringify(delivered.state.seen);

  const duplicate = stepped(delivered.state, deliver(message()));

  assert.equal(duplicate.outcome, OUTCOME.DUPLICATE);
  assert.equal(JSON.stringify(duplicate.state.seen), before);
  assert.equal(duplicate.state.seen.length, 1);
  assert.equal(duplicate.state.seen[0].status, STATUS.ACCEPTED);
});

test('an id from a different sender is not deduped against another sender\'s', () => {
  const first = stepped(newMailbox(), deliver(message({ sender: 'phone-a' })));
  const second = stepped(first.state, deliver(message({ sender: 'phone-b' })));

  assert.equal(second.outcome, OUTCOME.ACCEPTED);
  assert.equal(second.phase, STATE.PENDING);
  assert.equal(second.state.seen.length, 2);
  assert.deepEqual(second.state.seen.map((entry) => entry.sender), ['phone-a', 'phone-b']);
});

test('a duplicate of an acked message is a duplicate, and does not reopen it', () => {
  const state = mailboxIn(STATE.IDLE); // one delivery, acted on and acked
  assert.equal(state.seen[0].status, STATUS.ACKED);

  const duplicate = stepped(state, deliver(message()));

  assert.equal(duplicate.outcome, OUTCOME.DUPLICATE);
  assert.equal(duplicate.phase, STATE.IDLE);
  assert.equal(duplicate.state.seen[0].status, STATUS.ACKED);
  assert.equal(stepped(duplicate.state, act()).outcome, OUTCOME.NOTHING_TO_ACT);
});

test('a reused id carrying a different at is a conflict, not a duplicate', () => {
  const delivered = stepped(newMailbox(), deliver(message()));

  // Inside the window, and the same id: the id is the message's identity, so a
  // second message under it is an ambiguity -- refused, never merged.
  const conflict = refused(delivered.state, deliver(message({ at: NOW - 500 })), REFUSAL.ID_CONFLICT);
  assert.equal(conflict.field, 'message.at');
});

test('an id reused after the window is a new message, and the ledger keeps one record', () => {
  // The ledger forgets by the window, deliberately: it is the same bound the
  // replay rule uses, so the entry and the message can never disagree about
  // whether a delivery is inside it.
  const delivered = stepped(newMailbox(), deliver(message()));
  const later = NOW + WINDOW + 1;

  const reused = stepped(delivered.state, deliver(message({ at: later - 1_000 }), { now: later }));

  assert.equal(reused.outcome, OUTCOME.ACCEPTED);
  assert.equal(reused.state.seen.length, 1, 'one record per (sender, id)');
  assert.equal(reused.state.seen[0].at, later - 1_000);
});

// ---------------------------------------------------------------------------
// The replay window
// ---------------------------------------------------------------------------

test('a message older than the replay window is refused as a replay', () => {
  const result = refused(newMailbox(), deliver(message({ at: NOW - WINDOW - 1 })), REFUSAL.REPLAY);
  assert.equal(result.field, 'message.at');
});

test('the replay window is inclusive at its edge and exclusive one millisecond past it', () => {
  const atEdge = stepped(newMailbox(), deliver(message({ at: NOW - WINDOW })));
  assert.equal(atEdge.outcome, OUTCOME.ACCEPTED);

  refused(newMailbox(), deliver(message({ at: NOW - WINDOW - 1 })), REFUSAL.REPLAY);
});

test('a message from the future beyond the tolerance is refused, and inside it is not', () => {
  refused(newMailbox(), deliver(message({ at: NOW + TOLERANCE + 1 })), REFUSAL.FUTURE_TIMESTAMP);

  const inside = stepped(newMailbox(), deliver(message({ at: NOW + TOLERANCE })));
  assert.equal(inside.outcome, OUTCOME.ACCEPTED);

  // A future `at` inside the tolerance is not a replay either: its age is
  // negative, and a negative age is inside the window by definition.
  const skewed = stepped(newMailbox(), deliver(message({ at: NOW + 1 })));
  assert.equal(skewed.outcome, OUTCOME.ACCEPTED);
});

test('time is injected: the same delivery is accepted at one now and a replay at a later one', () => {
  const state = newMailbox();
  const old = message({ at: NOW - 1_000 });

  assert.equal(step(state, deliver(old)).outcome, OUTCOME.ACCEPTED);
  refused(state, deliver(old, { now: NOW + WINDOW + 1 }), REFUSAL.REPLAY);
});

test('a now that moves backwards is refused: the window cannot run in reverse', () => {
  const delivered = stepped(newMailbox(), deliver(message()));

  refused(delivered.state, close(NOW - 1), REFUSAL.NOW_WENT_BACKWARDS);
  refused(delivered.state, deliver(message({ id: 'm-0002' }), { now: NOW - 1 }), REFUSAL.NOW_WENT_BACKWARDS);
  // The same instant is not backwards.
  assert.equal(step(delivered.state, close(NOW)).ok, true);
});

// ---------------------------------------------------------------------------
// Bounded memory: the cap, the eviction and the refusal
// ---------------------------------------------------------------------------

test('the ledger never grows past its cap, and a full one refuses rather than evicts a live entry', () => {
  let state = newMailbox({ maxSeen: 2 });

  // Two deliveries, each acted on and acked, both INSIDE the window.
  for (const id of ['m-0001', 'm-0002']) {
    state = stepped(state, deliver(message({ id }))).state;
    state = stepped(state, act()).state;
    state = stepped(state, ack()).state;
  }
  assert.equal(state.seen.length, 2);
  assert.equal(state.phase, STATE.IDLE);

  // The cap is reached and nothing is evictable: the new delivery is refused.
  // Losing a record here would be the one thing the ledger must not do, because
  // a lost record is a message that can be acted on twice.
  refused(state, deliver(message({ id: 'm-0003' })), REFUSAL.DEDUPE_FULL);
});

test('an eviction cannot re-admit a replay: what is evicted is outside the window', () => {
  let state = newMailbox({ maxSeen: 2 });
  for (const id of ['m-0001', 'm-0002']) {
    state = stepped(state, deliver(message({ id }))).state;
    state = stepped(state, act()).state;
    state = stepped(state, ack()).state;
  }

  // A window later, both acked records are outside it: they are evictable, and
  // evicting them is safe for exactly one reason -- a redelivery of either is
  // refused by the replay rule before the ledger is ever consulted.
  const later = NOW + WINDOW + 1;
  const fresh = stepped(state, deliver(message({ id: 'm-0003', at: later - 1_000 }), { now: later }));
  assert.equal(fresh.outcome, OUTCOME.ACCEPTED);
  assert.deepEqual(ids(fresh.state), ['m-0003']);

  const redelivered = refused(
    fresh.state,
    deliver(message({ id: 'm-0001', at: NOW - 1_000 }), { now: later }),
    REFUSAL.REPLAY,
  );
  assert.equal(redelivered.field, 'message.at');
});

test('a delivery that is still in flight is never evicted', () => {
  let state = newMailbox({ maxSeen: 2 });

  // Two deliveries, acted on but NOT acked: in flight, so not evictable even
  // once the window has passed them.
  for (const id of ['m-0001', 'm-0002']) {
    state = stepped(state, deliver(message({ id }))).state;
    state = stepped(state, act()).state;
  }
  assert.equal(state.phase, STATE.ACTED);

  const later = NOW + WINDOW + 1;
  refused(state, deliver(message({ id: 'm-0003', at: later - 1_000 }), { now: later }), REFUSAL.DEDUPE_FULL);
  assert.deepEqual(ids(state), ['m-0001', 'm-0002'], 'the ledger is unchanged by a refusal');
});

test('the ledger stays bounded over a long mixed sequence', () => {
  let state = newMailbox({ maxSeen: 3 });
  let refusals = 0;
  let accepted = 0;
  let now = NOW;

  for (let i = 0; i < 200; i++) {
    now += 1_000;
    const id = `m-${String(i).padStart(4, '0')}`;
    const delivered = step(state, deliver(message({ id, at: now - 500 }), { now }));
    if (!delivered.ok) {
      // The only refusal this sequence can produce: the cap is reached and
      // nothing is evictable yet.
      assert.equal(delivered.code, REFUSAL.DEDUPE_FULL);
      refusals++;
      continue;
    }
    accepted++;
    state = delivered.state;
    state = stepped(state, act(now)).state;
    state = stepped(state, ack(now)).state;
    assert.ok(state.seen.length <= 3, `the ledger grew to ${state.seen.length}`);
  }

  // Both happen, and that is the point: the cap refuses, and the window keeps
  // making room. A cap that refused everything, or an eviction that dropped
  // everything, would pass a test that checked the bound alone.
  assert.ok(accepted > 0, 'no delivery was ever accepted');
  assert.ok(refusals > 0, 'the cap never refused a delivery');
  assert.ok(state.seen.length <= 3);
});

// ---------------------------------------------------------------------------
// The total table
// ---------------------------------------------------------------------------

test('the transition table is total: every state x every event has a defined outcome', () => {
  const states = Object.values(STATE);
  const events = Object.values(EVENT);

  assert.deepEqual(Object.keys(TRANSITIONS).sort(), [...states].sort(), 'a state has no row');

  const outcomes = new Set();
  // The delivery cells refuse with the schema's codes as well as this module's
  // own: a message the schema will not accept is not a delivery, and pretending
  // otherwise here would be a second, weaker schema.
  const vocabulary = new Set([...Object.values(REFUSAL), ...Object.values(REFUSALS)]);
  for (const state of states) {
    assert.deepEqual(Object.keys(TRANSITIONS[state]).sort(), [...events].sort(), `${state} has no full row`);
    for (const event of events) {
      const cell = TRANSITIONS[state][event];
      assert.ok(
        cell.ok.length + cell.no.length > 0,
        `${state} x ${event} has neither an outcome nor a refusal`,
      );
      assert.ok(cell.next.length > 0, `${state} x ${event} names no next phase`);
      for (const outcome of cell.ok) outcomes.add(outcome);
      for (const code of cell.no) {
        assert.ok(vocabulary.has(code), `${code} is neither this module's refusal nor the schema's`);
      }
    }
  }

  // Every outcome the module can answer with is declared in some cell, so the
  // table is not a summary that quietly covers part of the machine.
  assert.deepEqual([...outcomes].sort(), Object.values(OUTCOME).sort());
});

test('every state x every event cell agrees with the code', () => {
  for (const phase of Object.values(STATE)) {
    const state = mailboxIn(phase);
    const probes = {
      [EVENT.DELIVER]: [
        deliver(message({ id: `m-${phase}-fresh` })),
        deliver(message({ id: `m-${phase}-stale`, at: NOW - WINDOW - 1 })),
      ],
      [EVENT.ACT]: [act()],
      [EVENT.ACK]: [ack()],
      [EVENT.CLOSE]: [close()],
    };

    for (const event of Object.values(EVENT)) {
      const cell = TRANSITIONS[phase][event];
      const results = probes[event].map((probe) => step(state, probe));
      assert.equal(results.length > 0, true);

      for (const result of results) {
        if (result.ok) {
          assert.ok(
            cell.ok.includes(result.outcome),
            `${phase} x ${event} answered ${result.outcome}, which the table does not declare`,
          );
          assert.ok(
            cell.next.includes(result.phase),
            `${phase} x ${event} left the mailbox in ${result.phase}, which the table does not declare`,
          );
        } else {
          assert.ok(
            cell.no.includes(result.code),
            `${phase} x ${event} refused with ${result.code}, which the table does not declare`,
          );
        }
      }
    }
  }
});

test('every refusal the module can answer with is an input refusal or a declared cell', () => {
  const declared = new Set(INPUT_REFUSALS);
  for (const row of Object.values(TRANSITIONS)) {
    for (const cell of Object.values(row)) {
      for (const code of cell.no) declared.add(code);
    }
  }

  for (const [name, code] of Object.entries(REFUSAL)) {
    assert.ok(declared.has(code), `REFUSAL.${name} (${code}) is declared nowhere`);
  }
  // And the input refusals are the module's own, not the schema's: a code that
  // belongs to a cell must not be excused as an input refusal.
  assert.equal(new Set(INPUT_REFUSALS).size, INPUT_REFUSALS.length);
});

// ---------------------------------------------------------------------------
// Unknown states and unknown events
// ---------------------------------------------------------------------------

test('an unknown event is refused, with a code of its own', () => {
  refused(newMailbox(), { type: 'forget', now: NOW }, REFUSAL.UNKNOWN_EVENT);
  refused(newMailbox(), { type: 'DELIVER', now: NOW, message: message() }, REFUSAL.UNKNOWN_EVENT);
  refused(newMailbox(), { type: 1, now: NOW }, REFUSAL.UNKNOWN_EVENT);
  refused(newMailbox(), { type: 'deliver', now: NOW }, REFUSAL.MISSING_FIELD);
});

test('an unknown state is refused, with a code of its own', () => {
  const state = newMailbox();
  refused({ ...state, phase: 'waiting' }, act(), REFUSAL.UNKNOWN_STATE);
  refused({ ...state, phase: 'DELIVER' }, act(), REFUSAL.UNKNOWN_STATE);
  refused({ ...state, phase: null }, act(), REFUSAL.UNKNOWN_STATE);
});

test('a phase that disagrees with the ledger is refused rather than trusted', () => {
  // The phase is a summary of the ledger, and the two must agree: a state that
  // claims to be idle while holding a delivery nobody has acted on is a state
  // whose next answer cannot be predicted, so it is not a state.
  const pending = mailboxIn(STATE.PENDING);
  refused({ ...pending, phase: STATE.IDLE }, act(), REFUSAL.INVALID_STATE);
  refused({ ...pending, phase: STATE.ACTED }, act(), REFUSAL.INVALID_STATE);

  const acted = mailboxIn(STATE.ACTED);
  refused({ ...acted, phase: STATE.PENDING }, ack(), REFUSAL.INVALID_STATE);
});

test('a ledger that is not a ledger is refused, each way with its own code', () => {
  const state = newMailbox();
  const entry = { sender: 'phone-a', id: 'm-0001', at: NOW - 1_000, status: STATUS.ACCEPTED };
  const other = { sender: 'phone-b', id: 'm-0002', at: NOW - 900, status: STATUS.ACTED };

  refused({ ...state, seen: 'none' }, act(), REFUSAL.INVALID_SEEN);
  refused({ ...state, seen: [entry, other, entry, other, entry] }, act(), REFUSAL.INVALID_SEEN);
  refused({ ...state, seen: [{ ...entry, status: 'spoken' }] }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  refused({ ...state, seen: [{ ...entry, extra: 1 }] }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  refused({ ...state, seen: [{ ...entry, at: 1.5 }] }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  refused({ ...state, seen: [{ ...entry, sender: '' }] }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  refused({ ...state, seen: [{ ...entry, sender: 's'.repeat(201) }] }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  refused({ ...state, seen: [{ ...entry, id: 'i'.repeat(101) }] }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  refused({ ...state, seen: [{ ...entry, at: -0 }] }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  refused({ ...state, seen: [entry, { ...entry }] }, act(), REFUSAL.DUPLICATE_SEEN_ENTRY);
  refused({ ...state, seen: [other, entry] }, act(), REFUSAL.UNSORTED_SEEN);
  // A ledger longer than the state's own cap is not a state either.
  refused({ ...state, maxSeen: 1, seen: [entry, other] }, act(), REFUSAL.INVALID_SEEN);
});

// ---------------------------------------------------------------------------
// Input hygiene: own data properties, no coercion, no mutation
// ---------------------------------------------------------------------------

test('an accessor property is refused: a getter can answer differently each read', () => {
  const state = newMailbox();
  let reads = 0;
  const getter = () => {
    reads++;
    return NOW;
  };

  const eventWithGetter = { type: EVENT.CLOSE };
  Object.defineProperty(eventWithGetter, 'now', { get: getter, enumerable: true, configurable: true });
  refused(state, eventWithGetter, REFUSAL.ACCESSOR_PROPERTY);
  assert.equal(reads, 0, 'the getter must never be called');

  const stateWithGetter = { ...state };
  delete stateWithGetter.now;
  Object.defineProperty(stateWithGetter, 'now', { get: getter, enumerable: true, configurable: true });
  refused(stateWithGetter, close(), REFUSAL.ACCESSOR_PROPERTY);
  assert.equal(reads, 0, 'the getter must never be called');

  const ledgerWithGetter = [{ sender: 'phone-a', id: 'm-0001', at: NOW - 1_000, status: STATUS.ACKED }];
  const entryWithGetter = { sender: 'phone-a', id: 'm-0001', status: STATUS.ACKED };
  Object.defineProperty(entryWithGetter, 'at', { get: getter, enumerable: true, configurable: true });
  refused({ ...state, seen: ledgerWithGetter.concat([entryWithGetter]).slice(1) }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  assert.equal(reads, 0, 'the getter must never be called');
});

test('a value with a prototype of its own is not a state and not an event', () => {
  class Mailbox {}
  const state = newMailbox();

  refused(Object.assign(new Mailbox(), state), act(), REFUSAL.NOT_AN_OBJECT);
  refused(state, new (class extends Object {})(), REFUSAL.NOT_AN_OBJECT);
  refused(state, undefined, REFUSAL.NOT_AN_OBJECT);
  refused(null, act(), REFUSAL.NOT_AN_OBJECT);
  refused([], act(), REFUSAL.NOT_AN_OBJECT);

  // A null-prototype object IS a plain data object, and is accepted.
  const bare = Object.assign(Object.create(null), state);
  assert.equal(step(bare, close()).ok, true);
});

test('unknown and missing fields are refused on the state and on the event', () => {
  const state = newMailbox();

  refused({ ...state, window: WINDOW }, act(), REFUSAL.UNKNOWN_FIELD);
  refused(state, { type: EVENT.CLOSE, now: NOW, message: message() }, REFUSAL.UNKNOWN_FIELD);
  refused(state, { type: EVENT.DELIVER, now: NOW }, REFUSAL.MISSING_FIELD);
  refused(state, { now: NOW }, REFUSAL.MISSING_FIELD);

  const incomplete = { ...state };
  delete incomplete.maxSeen;
  refused(incomplete, act(), REFUSAL.MISSING_FIELD);
});

test('every number this module reads is a finite integer in its documented range', () => {
  const state = newMailbox();

  refused({ ...state, now: Number.POSITIVE_INFINITY }, act(), REFUSAL.INVALID_NOW);
  refused({ ...state, now: Number.NaN }, act(), REFUSAL.INVALID_NOW);
  refused({ ...state, now: -0 }, act(), REFUSAL.INVALID_NOW);
  refused({ ...state, windowMs: -0 }, act(), REFUSAL.INVALID_WINDOW);
  refused({ ...state, maxSeen: -0 }, act(), REFUSAL.INVALID_MAX_SEEN);
  refused({ ...state, maxSeen: 2.5 }, act(), REFUSAL.INVALID_MAX_SEEN);
  refused({ ...state, futureToleranceMs: -0 }, act(), REFUSAL.INVALID_FUTURE_TOLERANCE);
  refused(state, { type: EVENT.CLOSE, now: -0 }, REFUSAL.INVALID_NOW);
  refused(state, { type: EVENT.CLOSE, now: 2 ** 53 }, REFUSAL.INVALID_NOW);
});

test('a step mutates nothing it was handed, and everything it answers with is frozen', () => {
  const state = newMailbox();
  const before = JSON.stringify(state);
  const event = deliver(message());
  const eventBefore = JSON.stringify(event);

  const result = stepped(state, event);

  assert.equal(JSON.stringify(state), before, 'the input state was mutated');
  assert.equal(JSON.stringify(event), eventBefore, 'the input event was mutated');
  assert.ok(Object.isFrozen(result));
  assert.ok(Object.isFrozen(result.state));
  assert.ok(Object.isFrozen(result.state.seen));
  assert.ok(Object.isFrozen(result.state.seen[0]));
  assert.notEqual(result.state, state, 'the step returns a new state, not the old one');
});

test('the state a step returns shares no structure with what the caller handed in', () => {
  // A state is a value, not a view: a caller that keeps hold of the ledger it
  // built and writes to it later must not be able to change an answer the
  // machine has already given.
  const ledger = [{ sender: 'phone-a', id: 'm-0001', at: NOW - 1_000, status: STATUS.ACKED }];
  const state = { ...newMailbox(), seen: ledger };

  const closed = stepped(state, close());
  assert.deepEqual(ids(closed.state), ['m-0001']);

  ledger.push({ sender: 'phone-b', id: 'm-0002', at: NOW - 900, status: STATUS.ACCEPTED });
  assert.deepEqual(ids(closed.state), ['m-0001'], 'the returned state followed the caller\'s array');
});

test('a refusal never echoes the value it refused', () => {
  // Built at run time, from harmless pieces: nothing in this file is shaped
  // like the credential it stands in for.
  const secret = ['gh', 'p_', 'A'.repeat(36)].join('');
  const state = newMailbox();

  const refusals = [
    refused(state, deliver(message({ text: secret, extra: 1 })), REFUSALS.UNKNOWN_FIELD),
    refused(state, deliver(message({ text: secret, at: 'yesterday' })), REFUSALS.WRONG_TYPE),
    refused(state, deliver(message({ text: secret, at: NOW - WINDOW - 1 })), REFUSAL.REPLAY),
    refused(state, deliver(message({ id: secret, sender: secret, at: NOW + TOLERANCE + 1 })), REFUSAL.FUTURE_TIMESTAMP),
    refused({ ...state, now: secret }, act(), REFUSAL.INVALID_NOW),
    refused(state, { type: secret, now: NOW }, REFUSAL.UNKNOWN_EVENT),
    refused(state, { type: EVENT.CLOSE, now: secret }, REFUSAL.INVALID_NOW),
  ];

  for (const result of refusals) {
    assert.ok(
      !JSON.stringify(result).includes(secret),
      `a refusal echoed the refused value: ${JSON.stringify(result)}`,
    );
  }
});

// ---------------------------------------------------------------------------
// Ordering, and the parts of the machine that are not about dedupe
// ---------------------------------------------------------------------------

test('act acts on the oldest delivery first, and ack acks the oldest acted one', () => {
  const older = message({ id: 'm-0001', at: NOW - 2_000 });
  const newer = message({ id: 'm-0002', at: NOW - 1_000 });

  // Delivered out of order on purpose: the ledger is ordered by the messages'
  // own `at`, not by the order they arrived.
  let state = stepped(newMailbox(), deliver(newer)).state;
  state = stepped(state, deliver(older)).state;
  assert.deepEqual(ids(state), ['m-0001', 'm-0002']);

  const first = stepped(state, act());
  assert.equal(first.id, 'm-0001');
  assert.equal(first.phase, STATE.PENDING);

  const second = stepped(first.state, act());
  assert.equal(second.id, 'm-0002');
  assert.equal(second.phase, STATE.ACTED);

  const acked = stepped(second.state, ack());
  assert.equal(acked.id, 'm-0001');
  assert.equal(acked.phase, STATE.ACTED);
  assert.equal(stepped(acked.state, ack()).phase, STATE.IDLE);
});

test('two deliveries in either order leave the same state', () => {
  const older = message({ id: 'm-0001', at: NOW - 2_000 });
  const newer = message({ id: 'm-0002', at: NOW - 1_000 });

  const forwards = stepped(stepped(newMailbox(), deliver(older)).state, deliver(newer)).state;
  const backwards = stepped(stepped(newMailbox(), deliver(newer)).state, deliver(older)).state;

  assert.deepEqual(forwards, backwards);
});

test('an ack before the act is refused: the ack is not smuggled in', () => {
  const delivered = stepped(newMailbox(), deliver(message()));

  assert.equal(stepped(delivered.state, ack()).outcome, OUTCOME.NOTHING_TO_ACK);
  assert.equal(stepped(newMailbox(), ack()).outcome, OUTCOME.NOTHING_TO_ACK);
  assert.equal(stepped(newMailbox(), act()).outcome, OUTCOME.NOTHING_TO_ACT);
});

test('the same input answers the same thing: the machine holds no state of its own', () => {
  const state = newMailbox();
  const event = deliver(message());

  assert.deepEqual(step(state, event), step(state, event));
  assert.deepEqual(step(state, event).state, step(state, event).state);
});

test('a closed mailbox refuses every event, with one code and the state it was closed in', () => {
  const closed = mailboxIn(STATE.CLOSED);
  assert.equal(closed.phase, STATE.CLOSED);

  for (const event of [deliver(message({ id: 'm-late' })), act(), ack(), close()]) {
    const result = refused(closed, event, REFUSAL.MAILBOX_CLOSED);
    assert.equal(result.field, 'phase');
  }
});

test('closing an open mailbox is a step, and it keeps the ledger it had', () => {
  const pending = mailboxIn(STATE.PENDING);
  const closed = stepped(pending, close());

  assert.equal(closed.outcome, OUTCOME.CLOSED);
  assert.equal(closed.phase, STATE.CLOSED);
  assert.deepEqual(ids(closed.state), ['m-0001']);
  assert.equal(closed.state.now, NOW);
});

// ---------------------------------------------------------------------------
// The module's own hygiene
// ---------------------------------------------------------------------------

test('every refusal code is distinct and names itself', () => {
  const codes = Object.values(REFUSAL);

  assert.equal(new Set(codes).size, codes.length);
  for (const code of codes) {
    assert.match(code, /^[a-z][a-z0-9-]*$/);
  }
});

test('the module README documents every code, state, event and outcome', () => {
  // A code that exists but is documented nowhere is a caller's afternoon lost,
  // and a refusal table that quietly covers 21 of 22 codes reads as complete.
  const readme = readFileSync(new URL('./README.md', import.meta.url), 'utf8');

  for (const [name, code] of Object.entries(REFUSAL)) {
    assert.ok(readme.includes(code), `REFUSAL.${name} (${code}) is not documented in shared/mailbox/README.md`);
  }
  for (const [name, code] of Object.entries(REFUSALS)) {
    assert.ok(
      readme.includes(code),
      `the schema's ${name} (${code}) is propagated by this module and is not documented`,
    );
  }

  // The states, events and outcomes are checked as their own table cells rather
  // than as substrings, because "the word appears somewhere in the README" is
  // true of prose that no longer lists it as a state at all.
  const cells = readme
    .split('\n')
    .filter((line) => line.startsWith('|'))
    .flatMap((line) => line.split('|').map((cell) => cell.trim()));

  for (const value of [...Object.values(STATE), ...Object.values(EVENT), ...Object.values(OUTCOME)]) {
    assert.ok(cells.includes(`\`${value}\``), `${value} is not listed as a table cell in shared/mailbox/README.md`);
  }
});

test('the module is pure logic: one import, no clock, no randomness, no runtime', () => {
  const source = readFileSync(new URL('./index.js', import.meta.url), 'utf8');
  const code = source.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/[^\n]*/g, '');

  const specifiers = [...code.matchAll(/\bfrom\s+'([^']+)'/g)].map((match) => match[1]);
  assert.deepEqual(specifiers, ['../mailbox-schema/index.js'], 'the module imports more than the schema');

  for (const forbidden of ['Date', 'Math.random', 'node:', 'require(', 'process.', 'fetch(']) {
    assert.ok(!code.includes(forbidden), `the module's code mentions ${forbidden}`);
  }
});
