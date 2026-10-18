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

import { MAX_CLOCK_SKEW, MAX_TIMESTAMP } from '../lease/index.js';
import { REFUSALS } from '../mailbox-schema/index.js';
import {
  DEFAULT_FUTURE_TOLERANCE_MS,
  DEFAULT_MAX_SEEN,
  DEFAULT_WINDOW_MS,
  EVENT,
  INPUT_REFUSALS,
  KIND,
  MAX_FORWARD_JUMP_MS,
  MAX_FUTURE_TOLERANCE_MS,
  MAX_PROMPT_TTL_MS,
  MAX_MAX_SEEN,
  MAX_WINDOW_MS,
  MIN_MAX_SEEN,
  MIN_WINDOW_MS,
  OUTCOME,
  REFUSAL,
  STATE,
  STATUS,
  TIME_MAX,
  TRANSITIONS,
  defaultMaxPerSender,
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
// The prompt TTL (D118's mailbox TTL, owner ruling 2026-10-17): longer than
// the command window, so a prompt can be accepted where a command is a replay.
const PROMPT_TTL = 10 * WINDOW;

/** The mailbox's options, as `initialState` takes them. */
function options(overrides = {}) {
  return {
    now: NOW,
    windowMs: WINDOW,
    maxSeen: MAX_SEEN,
    // The quota is lifted in the general tests (every sender may use the
    // whole ledger) and exercised on its own, with its default, below.
    maxPerSender: overrides.maxSeen ?? MAX_SEEN,
    promptTtlMs: PROMPT_TTL,
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

/**
 * A message with the schema's required fields, and an `at` inside the window.
 *
 * `mac` is required on every message (P6.3, §3.5) and this module never reads
 * it — the tag is the consumer's to check, with a key this package does not
 * hold, before a message is ever delivered — so the fixture carries a
 * mac-SHAPED placeholder: 64 lowercase hex characters that no key produces.
 * A test that wants a real tag over a real byte string uses the vectors in
 * mailbox-schema.
 */
function message(overrides = {}) {
  return {
    id: 'm-0001',
    sender: 'phone-a',
    text: 'run 42 finished',
    at: NOW - 1_000,
    mac: 'f'.repeat(64),
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
    maxPerSender: undefined,
    futureToleranceMs: undefined,
  });

  assert.equal(state.windowMs, DEFAULT_WINDOW_MS);
  assert.equal(state.maxSeen, DEFAULT_MAX_SEEN);
  assert.equal(state.maxPerSender, DEFAULT_MAX_SEEN / 4);
  assert.equal(state.maxPerSender, defaultMaxPerSender(DEFAULT_MAX_SEEN));
  assert.equal(defaultMaxPerSender(1), 1, 'the quota is at least one');
  assert.equal(defaultMaxPerSender(7), 1);
  assert.equal(defaultMaxPerSender(8), 2);
  assert.equal(state.futureToleranceMs, DEFAULT_FUTURE_TOLERANCE_MS);

  // The defaults are inside the ranges the module refuses to leave.
  assert.ok(DEFAULT_WINDOW_MS >= MIN_WINDOW_MS && DEFAULT_WINDOW_MS <= MAX_WINDOW_MS);
  assert.ok(DEFAULT_MAX_SEEN >= MIN_MAX_SEEN && DEFAULT_MAX_SEEN <= MAX_MAX_SEEN);
  assert.ok(DEFAULT_FUTURE_TOLERANCE_MS <= MAX_FUTURE_TOLERANCE_MS);
});

test('initialState refuses a config that is not this module\'s config', () => {
  const T = { promptTtlMs: PROMPT_TTL };
  const cases = [
    [null, REFUSAL.NOT_AN_OBJECT],
    [[], REFUSAL.NOT_AN_OBJECT],
    [{}, REFUSAL.MISSING_FIELD],
    [{ now: NOW }, REFUSAL.MISSING_FIELD],
    [{ now: TIME_MAX + 1, ...T }, REFUSAL.INVALID_NOW],
    [{ now: NOW, promptTtlMs: MIN_WINDOW_MS - 1 }, REFUSAL.INVALID_PROMPT_TTL],
    [{ now: NOW, promptTtlMs: MAX_PROMPT_TTL_MS + 1 }, REFUSAL.INVALID_PROMPT_TTL],
    [{ now: NOW, promptTtlMs: -0 }, REFUSAL.INVALID_PROMPT_TTL],
    [{ now: NOW, maxSeen: 4, maxPerSender: 0, ...T }, REFUSAL.INVALID_MAX_PER_SENDER],
    [{ now: NOW, maxSeen: 4, maxPerSender: 5, ...T }, REFUSAL.INVALID_MAX_PER_SENDER],
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
    [{ now: NOW, futureToleranceMs: -1, ...T }, REFUSAL.INVALID_FUTURE_TOLERANCE],
    [{ now: NOW, futureToleranceMs: -0, ...T }, REFUSAL.INVALID_FUTURE_TOLERANCE],
    [{ now: NOW, futureToleranceMs: MAX_FUTURE_TOLERANCE_MS + 1, ...T }, REFUSAL.INVALID_FUTURE_TOLERANCE],
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
    // An unknown key is reported as the message, never by the key's own name:
    // the key is the writer's choice, and a refusal is logged.
    [message({ extra: 1 }), REFUSALS.UNKNOWN_FIELD, 'message'],
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

test('the ledger never grows past its cap: a full one refuses rather than forgets a record inside the horizon', () => {
  let state = newMailbox({ maxSeen: 2 });

  // Two deliveries, each acted on and acked, both INSIDE the horizon.
  for (const id of ['m-0001', 'm-0002']) {
    state = stepped(state, deliver(message({ id }))).state;
    state = stepped(state, act()).state;
    state = stepped(state, ack()).state;
  }
  assert.equal(state.seen.length, 2);
  assert.equal(state.phase, STATE.IDLE);

  // The cap is reached and nothing is past the horizon: refused. Forgetting an
  // acked record here would let its id be acted on again inside the horizon.
  refused(state, deliver(message({ id: 'm-0003' })), REFUSAL.DEDUPE_FULL);
  assert.equal(state.evictedThrough, 0);

  // Past the horizon they are evicted, the watermark rises, and there is room.
  const past = NOW + PROMPT_TTL + 1_001;
  const fresh = stepped(state, deliver(message({ id: 'm-0003', at: past }), { now: past }));
  assert.deepEqual(ids(fresh.state), ['m-0003']);
  assert.equal(fresh.state.evictedThrough, NOW - 1_000);
});

test('an eviction cannot re-admit a replay: what is evicted is outside the window', () => {
  // promptTtlMs = WINDOW, so the eviction horizon is the command window.
  let state = newMailbox({ maxSeen: 2, promptTtlMs: WINDOW });
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
    now += 10_000;
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

  // Both happen, and that is the point: the cap refuses while the records are
  // inside the horizon, and the horizon keeps making room. A cap that refused
  // everything, or an eviction that dropped everything, would pass a test
  // that checked the bound alone.
  assert.ok(accepted > 3, 'the horizon never made room');
  assert.ok(refusals > 0, 'the cap never refused a delivery');
  assert.ok(state.seen.length <= 3);
  assert.ok(state.evictedThrough > NOW, 'the watermark never moved');
  assert.ok(state.evictedThrough <= state.now - PROMPT_TTL);
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

// The EXPECTED table, written here by hand and NOT imported from the module:
// a test that read the module's own TRANSITIONS could only ever agree with it.
// Each cell names the outcomes it may answer with, the phases it may leave the
// mailbox in, and whether it refuses with the delivery refusals.
const DELIVERY_CODES = Object.freeze([
  'not-an-object',
  'unknown-field',
  'missing-field',
  'negative-zero',
  'zero-timestamp',
  'replay',
  'expired',
  'future-timestamp',
  'id-conflict',
  'dedupe-full',
  'sender-full',
  ...Object.values(REFUSALS),
]);
// The delivery refusals that are this module's own decision (not the schema's
// and not an input refusal): each must actually be produced by the
// enumeration below, so the table cannot list a code nothing raises.
const MODULE_DELIVERY_CODES = Object.freeze([
  'negative-zero',
  'zero-timestamp',
  'replay',
  'expired',
  'future-timestamp',
  'id-conflict',
  'dedupe-full',
  'sender-full',
]);
const CLOSED_CELL = { ok: [], next: ['closed'], no: ['mailbox-closed'] };
const EXPECTED = {
  idle: {
    deliver: { ok: ['accepted', 'duplicate'], next: ['pending', 'idle'], no: DELIVERY_CODES },
    act: { ok: ['nothing-to-act'], next: ['idle'], no: [] },
    ack: { ok: ['nothing-to-ack'], next: ['idle'], no: [] },
    close: { ok: ['closed'], next: ['closed'], no: [] },
  },
  pending: {
    deliver: { ok: ['accepted', 'duplicate'], next: ['pending'], no: DELIVERY_CODES },
    act: { ok: ['acted', 'stale-pending'], next: ['acted', 'pending'], no: [] },
    // deliver a, act, deliver b: the mailbox is pending AND holds an acted
    // record, so an ack there acks it (the cell the first table got wrong).
    ack: { ok: ['acked', 'nothing-to-ack'], next: ['pending'], no: [] },
    close: { ok: ['closed'], next: ['closed'], no: [] },
  },
  acted: {
    deliver: { ok: ['accepted', 'duplicate'], next: ['pending', 'acted'], no: DELIVERY_CODES },
    act: { ok: ['nothing-to-act'], next: ['acted'], no: [] },
    ack: { ok: ['acked'], next: ['idle', 'acted'], no: [] },
    close: { ok: ['closed'], next: ['closed'], no: [] },
  },
  closed: { deliver: CLOSED_CELL, act: CLOSED_CELL, ack: CLOSED_CELL, close: CLOSED_CELL },
};

const sorted = (values) => [...new Set(values)].sort();

/** A ledger record, as a literal. */
function record(sender, id, at, status, kind = KIND.COMMAND) {
  return { sender, id, at, kind, status };
}

/** The canonical order, restated here rather than trusted from the module. */
function canonical(entries) {
  const key = (entry) => [entry.at, entry.sender, entry.id];
  return [...entries].sort((left, right) => {
    const [a, b] = [key(left), key(right)];
    for (let i = 0; i < 3; i++) {
      if (a[i] !== b[i]) return a[i] < b[i] ? -1 : 1;
    }
    return 0;
  });
}

/** The phase a ledger implies, restated independently of the module. */
function phaseOf(entries) {
  if (entries.some((entry) => entry.status === STATUS.ACCEPTED)) return 'pending';
  if (entries.some((entry) => entry.status === STATUS.ACTED)) return 'acted';
  return 'idle';
}

/** A literal state: built by hand, so every composition is reachable. */
function stateWith(entries, overrides = {}) {
  const seen = canonical(entries);
  return {
    phase: phaseOf(seen),
    now: NOW,
    windowMs: WINDOW,
    maxSeen: MAX_SEEN,
    maxPerSender: 2,
    promptTtlMs: PROMPT_TTL,
    futureToleranceMs: TOLERANCE,
    evictedThrough: 0,
    seen,
    ...overrides,
  };
}

const OLD = NOW - WINDOW - 10; // a command's window has passed it
const DEAD = NOW - PROMPT_TTL - 10; // a prompt's TTL has passed it too
const { ACCEPTED: P, ACTED: A, ACKED: K } = STATUS;

/** Every ledger composition a phase can have, as the review listed them. */
const COMPOSITIONS = {
  'empty': [],
  'pending-only': [record('a', 'p1', NOW - 1_000, P)],
  'acted-only': [record('a', 't1', NOW - 1_000, A)],
  'two pending': [record('a', 'p1', NOW - 2_000, P), record('b', 'p2', NOW - 1_000, P)],
  'two acted': [record('a', 't1', NOW - 2_000, A), record('b', 't2', NOW - 1_000, A)],
  'acked-only': [record('a', 'k1', NOW - 1_000, K)],
  'mixed': [record('a', 'k1', NOW - 3_000, K), record('b', 't1', NOW - 2_000, A), record('a', 'p1', NOW - 1_000, P)],
  'acted-and-pending': [record('a', 't1', NOW - 2_000, A), record('b', 'p1', NOW - 1_000, P)],
  'at-cap pending, with acked': [record('a', 'c1', NOW - 4_000, K), record('a', 'c2', NOW - 3_000, P), record('b', 'c3', NOW - 2_000, K), record('b', 'c4', NOW - 1_000, A)],
  'at-cap acted, with acked': [record('a', 'c1', NOW - 4_000, K), record('a', 'c2', NOW - 3_000, A), record('b', 'c3', NOW - 2_000, K), record('b', 'c4', NOW - 1_000, A)],
  'at-cap pending, all in flight': [record('a', 'c1', NOW - 4_000, A), record('a', 'c2', NOW - 3_000, P), record('b', 'c3', NOW - 2_000, P), record('b', 'c4', NOW - 1_000, A)],
  'at-cap acted, all in flight': [record('a', 'c1', NOW - 4_000, A), record('a', 'c2', NOW - 3_000, A), record('b', 'c3', NOW - 2_000, A), record('b', 'c4', NOW - 1_000, A)],
  'at-cap acked': [record('a', 'c1', NOW - 4_000, K), record('a', 'c2', NOW - 3_000, K), record('b', 'c3', NOW - 2_000, K), record('b', 'c4', NOW - 1_000, K)],
  'at-cap acked, all stale': [record('a', 'c1', OLD - 3, K), record('a', 'c2', OLD - 2, K), record('b', 'c3', OLD - 1, K), record('b', 'c4', OLD, K)],
  'sender with acked records only, idle': [record('a', 'q1', NOW - 2_000, K), record('a', 'q2', NOW - 1_000, K)],
  'sender at quota, pending': [record('a', 'q1', NOW - 2_000, P), record('a', 'q2', NOW - 1_000, A)],
  'sender at quota, acted': [record('a', 'q1', NOW - 2_000, A), record('a', 'q2', NOW - 1_000, A)],
  'stale accepted': [record('a', 's1', OLD, P)],
  'stale acted': [record('a', 's1', OLD, A)],
  'stale acked': [record('a', 's1', OLD, K)],
  'stale mixed': [record('a', 's1', OLD - 2, K), record('b', 's2', OLD - 1, A), record('c', 's3', OLD, P)],
  'prompt past the command window, inside its TTL': [record('a', 'pr', OLD, P, KIND.PROMPT)],
  'prompt past its TTL': [record('a', 'pr', DEAD, P, KIND.PROMPT)],
  'acked prompt inside its TTL': [record('a', 'pr', OLD, K, KIND.PROMPT)],
  // Round 4: a record dated inside the future tolerance, alone and in a full
  // ledger, and ledgers whose watermark is already set.
  'future-dated acked': [record('a', 'f1', NOW + TOLERANCE, K)],
  'future-dated acked at cap': [record('a', 'k1', NOW - 3_000, K), record('b', 'k2', NOW - 2_000, K), record('c', 'k3', NOW - 1_000, K), record('d', 'f1', NOW + TOLERANCE, K)],
  'watermark set, idle': [record('a', 'k1', NOW - 1_000, K)],
  'watermark set, pending': [record('a', 'p1', NOW - 1_000, P)],
  'watermark set, acted': [record('a', 't1', NOW - 1_000, A)],
  'watermark set, empty': [],
};

/** The state fields a composition sets beside its ledger. */
const WATERMARK = NOW - PROMPT_TTL; // the latest a watermark may be at NOW
const COMPOSITION_STATE = {
  'watermark set, idle': { evictedThrough: WATERMARK },
  'watermark set, pending': { evictedThrough: WATERMARK },
  'watermark set, acted': { evictedThrough: WATERMARK },
  'watermark set, empty': { evictedThrough: WATERMARK },
};

/** Every event worth sending to a composition. */
function probesFor(entries) {
  const events = [
    ['deliver', deliver(message({ sender: 'c', id: 'fresh' }))],
    ['deliver', deliver(message({ sender: 'a', id: 'fresh-a' }))],
    ['deliver', deliver(message({ sender: 'c', id: 'fresh-p', at: NOW - WINDOW - 5 }), { kind: KIND.PROMPT })],
    ['deliver', deliver(message({ id: 'old', at: NOW - WINDOW - 1 }))],
    ['deliver', deliver(message({ id: 'dead', at: NOW - PROMPT_TTL - 1 }), { kind: KIND.PROMPT })],
    // Aged exactly the TTL: inside the window, and at the watermark when set.
    ['deliver', deliver(message({ sender: 'c', id: 'edge', at: NOW - PROMPT_TTL }), { kind: KIND.PROMPT })],
    ['deliver', deliver(message({ id: 'ahead', at: NOW + TOLERANCE + 1 }))],
    ['deliver', deliver(message({ id: 'zero', at: 0 }))],
    ['deliver', deliver(message({ id: 'negzero', at: -0 }))],
    ['deliver', deliver(message({ id: 'empty', text: '' }))],
    ['deliver', deliver(null)],
    ['act', act()],
    ['ack', ack()],
    ['close', close()],
  ];
  for (const entry of entries) {
    const other = entry.kind === KIND.PROMPT ? KIND.COMMAND : KIND.PROMPT;
    const same = message({ sender: entry.sender, id: entry.id, at: entry.at });
    events.push(['deliver', deliver(same, { kind: entry.kind })]);
    events.push(['deliver', deliver(same, { kind: other })]);
    events.push(['deliver', deliver(message({ sender: entry.sender, id: entry.id, at: NOW - 1 }), { kind: entry.kind })]);
  }
  return events;
}

test('the declared table is the expected one, written independently of the module', () => {
  assert.deepEqual(sorted(Object.keys(TRANSITIONS)), sorted(Object.keys(EXPECTED)));
  for (const [phase, row] of Object.entries(EXPECTED)) {
    assert.deepEqual(sorted(Object.keys(TRANSITIONS[phase])), sorted(Object.keys(row)), `${phase}'s row`);
    for (const [event, cell] of Object.entries(row)) {
      const declared = TRANSITIONS[phase][event];
      assert.deepEqual(sorted(declared.ok), sorted(cell.ok), `${phase} x ${event}: outcomes`);
      assert.deepEqual(sorted(declared.next), sorted(cell.next), `${phase} x ${event}: next phases`);
      assert.deepEqual(sorted(declared.no), sorted(cell.no), `${phase} x ${event}: refusals`);
    }
  }
});

test('every ledger composition x every event agrees with the expected table, and every cell is reached', () => {
  const observed = {};
  for (const phase of Object.keys(EXPECTED)) {
    observed[phase] = {};
    for (const event of Object.keys(EXPECTED[phase])) observed[phase][event] = { ok: [], next: [], no: [] };
  }

  let probes = 0;
  for (const [name, entries] of Object.entries(COMPOSITIONS)) {
    const open = stateWith(entries, COMPOSITION_STATE[name] ?? {});
    // The composition must be a state the module reads, or this test proves
    // nothing about it.
    assert.equal(step(open, close()).ok, true, `${name} is not a readable state`);

    for (const state of [open, { ...open, phase: 'closed' }]) {
      for (const [event, probe] of probesFor(entries)) {
        probes++;
        const cell = EXPECTED[state.phase][event];
        const seen = observed[state.phase][event];
        const result = step(state, probe);
        const where = `${name} (${state.phase}) x ${event} ${JSON.stringify(probe.message ?? null)}`;
        if (result.ok) {
          assert.ok(cell.ok.includes(result.outcome), `${where} answered ${result.outcome}`);
          assert.ok(cell.next.includes(result.phase), `${where} left the mailbox ${result.phase}`);
          assert.equal(result.state.phase, result.phase, `${where}: the answer and its state disagree`);
          // And what it answers with is a state the module reads back.
          const back = step(result.state, close());
          assert.ok(back.ok || back.code === 'mailbox-closed', `${where} returned an unreadable state`);
          seen.ok.push(result.outcome);
          seen.next.push(result.phase);
        } else {
          assert.ok(cell.no.includes(result.code), `${where} refused ${result.code}`);
          // A refusal has no state: the mailbox stays in the phase it was in,
          // which the cell must therefore declare as a next phase.
          assert.ok(cell.next.includes(state.phase), `${where}: a refusal must leave ${state.phase}`);
          seen.no.push(result.code);
          seen.next.push(state.phase);
        }
      }
    }
  }
  assert.ok(probes > 500, `only ${probes} probes ran`);

  // The other direction: every outcome and next phase the table declares is
  // actually produced, so the table cannot over-declare a cell either.
  const raised = new Set();
  for (const [phase, row] of Object.entries(EXPECTED)) {
    for (const [event, cell] of Object.entries(row)) {
      const seen = observed[phase][event];
      assert.deepEqual(sorted(seen.ok), sorted(cell.ok), `${phase} x ${event}: outcomes reached`);
      assert.deepEqual(sorted(seen.next), sorted(cell.next), `${phase} x ${event}: phases reached`);
      if (cell === CLOSED_CELL) assert.deepEqual(sorted(seen.no), ['mailbox-closed']);
      if (event === 'deliver' && phase !== 'closed') {
        for (const code of seen.no) raised.add(code);
        for (const code of MODULE_DELIVERY_CODES) {
          assert.ok(seen.no.includes(code), `${phase} x deliver never refused with ${code}`);
        }
      }
    }
  }
  assert.ok(raised.size >= MODULE_DELIVERY_CODES.length);
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
  const entry = { sender: 'phone-a', id: 'm-0001', at: NOW - 1_000, kind: KIND.COMMAND, status: STATUS.ACCEPTED };
  const other = { sender: 'phone-b', id: 'm-0002', at: NOW - 900, kind: KIND.PROMPT, status: STATUS.ACTED };

  refused({ ...state, seen: 'none' }, act(), REFUSAL.INVALID_SEEN);
  refused({ ...state, seen: [entry, other, entry, other, entry] }, act(), REFUSAL.INVALID_SEEN);
  refused({ ...state, seen: [{ ...entry, status: 'spoken' }] }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  refused({ ...state, seen: [{ ...entry, extra: 1 }] }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  refused({ ...state, seen: [{ ...entry, at: 1.5 }] }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  refused({ ...state, seen: [{ ...entry, sender: '' }] }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  refused({ ...state, seen: [{ ...entry, sender: 5 }] }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  refused({ ...state, seen: [{ ...entry, id: 5 }] }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  refused({ ...state, seen: [{ ...entry, id: '' }] }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  refused({ ...state, seen: [{ ...entry, sender: 's'.repeat(201) }] }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  refused({ ...state, seen: [{ ...entry, id: 'i'.repeat(101) }] }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  refused({ ...state, seen: [{ ...entry, at: -0 }] }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  refused({ ...state, seen: [{ ...entry, at: TIME_MAX + 1 }] }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  refused({ ...state, seen: [{ ...entry, kind: 'reply' }] }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  const kindless = { ...entry };
  delete kindless.kind;
  // A record without a kind is refused rather than read as a command: which
  // window it is judged by is not a guess.
  refused({ ...state, seen: [kindless] }, act(), REFUSAL.INVALID_SEEN_ENTRY);
  refused({ ...state, seen: [entry, { ...entry }] }, act(), REFUSAL.DUPLICATE_SEEN_ENTRY);
  refused({ ...state, seen: [other, entry] }, act(), REFUSAL.UNSORTED_SEEN);
  // A ledger longer than the state's own cap is not a state either.
  refused({ ...state, maxSeen: 1, maxPerSender: 1, seen: [entry, other] }, act(), REFUSAL.INVALID_SEEN);
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

  const ledgerWithGetter = [{ sender: 'phone-a', id: 'm-0001', at: NOW - 1_000, kind: KIND.COMMAND, status: STATUS.ACKED }];
  const entryWithGetter = { sender: 'phone-a', id: 'm-0001', kind: KIND.COMMAND, status: STATUS.ACKED };
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
  refused({ ...state, maxPerSender: 0 }, act(), REFUSAL.INVALID_MAX_PER_SENDER);
  refused({ ...state, maxPerSender: MAX_SEEN + 1 }, act(), REFUSAL.INVALID_MAX_PER_SENDER);
  refused({ ...state, promptTtlMs: MIN_WINDOW_MS - 1 }, act(), REFUSAL.INVALID_PROMPT_TTL);
  refused({ ...state, promptTtlMs: MAX_PROMPT_TTL_MS + 1 }, act(), REFUSAL.INVALID_PROMPT_TTL);
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
  const ledger = [{ sender: 'phone-a', id: 'm-0001', at: NOW - 1_000, kind: KIND.COMMAND, status: STATUS.ACKED }];
  const state = { ...newMailbox(), seen: ledger };

  const closed = stepped(state, close());
  assert.deepEqual(ids(closed.state), ['m-0001']);

  ledger.push({ sender: 'phone-b', id: 'm-0002', at: NOW - 900, kind: KIND.COMMAND, status: STATUS.ACCEPTED });
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

test('two senders whose names concatenate alike are still two pairs', () => {
  // The ledger's key is the pair (sender, id), and the two halves must not be
  // able to blur into each other: a key built by concatenation alone would
  // make ('ab', 'c') and ('a', 'bc') one pair, and refuse a ledger that is
  // perfectly well formed.
  const first = stepped(newMailbox(), deliver(message({ sender: 'ab', id: 'c' })));
  const second = stepped(first.state, deliver(message({ sender: 'a', id: 'bc' })));

  assert.equal(second.outcome, OUTCOME.ACCEPTED);
  assert.equal(second.state.seen.length, 2);
  // The state it returns must also be a state it would accept back.
  assert.equal(step(second.state, act()).ok, true);
});

test('every state a step returns is a state the next step accepts', () => {
  // A caller's whole contract in one line: a state handed back is a state taken
  // back. A delivery admitted by one rule and refused by another is an accepted
  // message nobody can act on, ack or close, so the round trip is asserted for
  // every edge the delivery rules have rather than for the ordinary case alone.
  const roundTrips = [
    [newMailbox(), message({ at: NOW - 1_000 })],
    [newMailbox(), message({ at: NOW })],
    [newMailbox(), message({ at: NOW + TOLERANCE })],
    [newMailbox(), message({ at: NOW - WINDOW })],
    [newMailbox(), message({ id: 'm-oldest', at: NOW - WINDOW + 1 })],
    [newMailbox({ now: 1 }), message({ at: 1 })],
    [newMailbox({ now: 1, windowMs: MIN_WINDOW_MS }), message({ id: 'm-first', at: 1 })],
  ];

  for (const [state, value] of roundTrips) {
    const now = state.now;
    const delivered = stepped(state, deliver(value, { now }));
    const acted = stepped(delivered.state, act(now));
    const acked = stepped(acted.state, ack(now));
    assert.equal(acked.phase, STATE.IDLE);
    assert.equal(step(acked.state, close(now)).ok, true, 'the ledger it kept is a ledger it reads');
  }

  // The other side of the same invariant: a timestamp the ledger cannot hold is
  // refused at the door rather than admitted into a state this module's own
  // reader rejects. `at: 0` is schema-valid (P0.2 allows a safe integer >= 0)
  // and is not a time the ledger will carry.
  const tiny = newMailbox({ now: 500, windowMs: MIN_WINDOW_MS });
  const zero = step(tiny, deliver(message({ at: 0 }), { now: 500 }));
  assert.equal(zero.ok, false, 'at: 0 was admitted into the ledger');
  assert.equal(zero.code, REFUSAL.ZERO_TIMESTAMP);
  assert.equal(zero.field, 'message.at');
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

  for (const value of [
    ...Object.values(STATE),
    ...Object.values(EVENT),
    ...Object.values(OUTCOME),
    ...Object.values(KIND),
  ]) {
    assert.ok(cells.includes(`\`${value}\``), `${value} is not listed as a table cell in shared/mailbox/README.md`);
  }
});

test('the module is pure logic: one import, no clock, no randomness, no runtime', () => {
  const source = readFileSync(new URL('./index.js', import.meta.url), 'utf8');
  const code = source.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/[^\n]*/g, '');

  const specifiers = [...code.matchAll(/\bfrom\s+'([^']+)'/g)].map((match) => match[1]);
  // The schema it speaks, and the lease's two time constants (its timestamp
  // ceiling and its clock skew), imported so the two modules cannot drift.
  assert.deepEqual(
    specifiers,
    ['../lease/index.js', '../mailbox-schema/index.js'],
    'the module imports more than the schema and the lease',
  );

  for (const forbidden of ['Date', 'Math.random', 'node:', 'require(', 'process.', 'fetch(']) {
    assert.ok(!code.includes(forbidden), `the module's code mentions ${forbidden}`);
  }
});

// ---------------------------------------------------------------------------
// Round 2 (independent review of 2026-10-17): the same id acted on once, at
// every age; the window's edges; the per-sender quota; the ledger's reader
// ---------------------------------------------------------------------------

test('a stale record that was acted on is never re-admitted under its id', () => {
  // The review's probe: deliver x at T0, act, deliver x again a window later
  // carrying a fresh `at`. The first round dropped the stale un-acked record
  // and accepted x, and the next act acted on x a second time.
  let state = stepped(newMailbox(), deliver(message({ at: NOW }))).state;
  state = stepped(state, act()).state;
  const later = NOW + WINDOW + 1;

  const again = refused(state, deliver(message({ at: later }), { now: later }), REFUSAL.ID_CONFLICT);
  assert.equal(again.field, 'message.at');
  assert.deepEqual(state.seen.map((entry) => entry.status), [STATUS.ACTED], 'the record is kept as it was');
  assert.equal(stepped(state, act(later)).outcome, OUTCOME.NOTHING_TO_ACT, 'x was acted on twice');
});

test('an acked record outside the window, not yet evicted, is a conflict and not a new message', () => {
  let state = mailboxIn(STATE.IDLE); // m-0001, acked
  const later = NOW + WINDOW + 1;
  refused(state, deliver(message({ at: later }), { now: later }), REFUSAL.ID_CONFLICT);
  // And the original redelivery is a replay, by the window, before the ledger.
  refused(state, deliver(message(), { now: later }), REFUSAL.REPLAY);
});

test('an accepted record the window has passed is kept, and surfaces as stale-pending', () => {
  let state = stepped(newMailbox(), deliver(message({ id: 'm-late', at: NOW }))).state;
  const later = NOW + WINDOW + 1;

  // Another delivery after the window: the un-acted record is not evicted.
  state = stepped(state, deliver(message({ id: 'm-next', at: later }), { now: later })).state;
  assert.deepEqual(ids(state), ['m-late', 'm-next']);

  const late = stepped(state, act(later));
  assert.equal(late.outcome, OUTCOME.STALE_PENDING, 'late work must be surfaced as late');
  assert.equal(late.id, 'm-late');
  assert.equal(late.phase, STATE.PENDING);
  // It is acted on once, like any other: it is not offered again.
  const next = stepped(late.state, act(later));
  assert.equal(next.outcome, OUTCOME.ACTED);
  assert.equal(next.id, 'm-next');
  assert.equal(stepped(next.state, act(later)).outcome, OUTCOME.NOTHING_TO_ACT);

  // At exactly the window's age it is not stale yet.
  const edge = stepped(stepped(newMailbox(), deliver(message({ at: NOW }))).state, act(NOW + WINDOW));
  assert.equal(edge.outcome, OUTCOME.ACTED);
});

test('a full ledger never drops an accepted record, at any age', () => {
  let state = stepped(newMailbox({ maxSeen: 1 }), deliver(message({ at: NOW }))).state;
  const muchLater = NOW + 100 * WINDOW;
  refused(state, deliver(message({ id: 'm-0002', at: muchLater }), { now: muchLater }), REFUSAL.DEDUPE_FULL);
  assert.equal(stepped(state, act(muchLater)).outcome, OUTCOME.STALE_PENDING);
});

test('an acked record at exactly the horizon\'s age is kept, and its redelivery is a duplicate', () => {
  // promptTtlMs = WINDOW, so the eviction horizon is exactly WINDOW.
  let state = stepped(newMailbox({ maxSeen: 3, promptTtlMs: WINDOW }), deliver(message({ at: NOW }))).state;
  state = stepped(state, act()).state;
  state = stepped(state, ack()).state;

  // Age exactly WINDOW: the message is still deliverable (the replay rule is
  // inclusive), so its record must still be there to call it a duplicate.
  const edge = NOW + WINDOW;
  state = stepped(state, deliver(message({ id: 'm-0002', at: edge }), { now: edge })).state;
  assert.deepEqual(ids(state), ['m-0001', 'm-0002'], 'an acked record was evicted at age == window');
  assert.equal(stepped(state, deliver(message({ at: NOW }), { now: edge })).outcome, OUTCOME.DUPLICATE);

  // One millisecond later it is evictable, and the redelivery is a replay.
  const past = edge + 1;
  state = stepped(state, deliver(message({ id: 'm-0003', at: past }), { now: past })).state;
  assert.deepEqual(ids(state), ['m-0002', 'm-0003']);
  refused(state, deliver(message({ at: NOW }), { now: past }), REFUSAL.REPLAY);
});

test('an acted record past the horizon is kept: the ack it is owed is never lost', () => {
  // The eviction rule is "acked AND strictly older than the horizon", and the
  // `acked` half is load-bearing in both directions. An `acted` record is work
  // this consumer still owes an ack for, so evicting it past the horizon loses
  // the ack, not just a bookkeeping row: the sender never learns the message
  // was acted on, and re-delivers it. The horizon here is the prompt TTL
  // (72 hours), longer than the command window, so an age past the horizon is
  // reachable without leaving the module's own bounds.
  const HOUR = 3_600_000;
  const TTL = 72 * HOUR;
  const later = NOW + 80 * HOUR; // past the horizon, and past the TTL itself

  let state = stepped(newMailbox({ maxSeen: 3, promptTtlMs: TTL }), deliver(message({ at: NOW }))).state;
  state = stepped(state, act()).state;
  assert.equal(state.seen[0].status, STATUS.ACTED);

  // A delivery long after the horizon: the acted record is not evicted with
  // it, whatever its age (it is not acked, so it is not evictable at all).
  state = stepped(state, deliver(message({ id: 'm-0002', at: later }), { now: later })).state;
  assert.deepEqual(ids(state), ['m-0001', 'm-0002'], 'an acted record past the horizon was evicted: its ack is lost');
  assert.equal(state.seen[0].status, STATUS.ACTED, 'the record is kept as it was');

  // And the ack it is owed still finds it: the outcome names m-0001, the one
  // that was acted on, not the newer pending delivery.
  const acked = stepped(state, ack(later));
  assert.equal(acked.outcome, OUTCOME.ACKED);
  assert.equal(acked.id, 'm-0001');
  assert.equal(acked.state.seen[0].status, STATUS.ACKED);
});


test('one sender cannot starve another: the per-sender quota', () => {
  // The default quota: a quarter of the cap. 8 records, 2 per sender.
  let state = newMailbox({ maxSeen: 8, maxPerSender: undefined });
  assert.equal(state.maxPerSender, 2);

  for (const id of ['a-1', 'a-2']) {
    state = stepped(state, deliver(message({ sender: 'phone-a', id }))).state;
  }
  const full = refused(state, deliver(message({ sender: 'phone-a', id: 'a-3' })), REFUSAL.SENDER_FULL);
  assert.equal(full.field, 'message.sender');

  // Another sender still has its own room.
  assert.equal(stepped(state, deliver(message({ sender: 'phone-b', id: 'b-1' }))).outcome, OUTCOME.ACCEPTED);

  // A consumer that stops acking does not change that: a window later A's
  // records are still un-acked (so never evicted) and A is still at its quota,
  // and B is still admitted.
  const later = NOW + WINDOW + 1;
  refused(state, deliver(message({ sender: 'phone-a', id: 'a-3', at: later }), { now: later }), REFUSAL.SENDER_FULL);
  assert.equal(
    stepped(state, deliver(message({ sender: 'phone-b', id: 'b-1', at: later }), { now: later })).outcome,
    OUTCOME.ACCEPTED,
  );
});

test('a sender at its quota, or a full ledger, never re-admits a replay and never evicts', () => {
  let state = newMailbox({ maxSeen: 8, maxPerSender: 2 });
  for (const id of ['a-1', 'a-2']) {
    state = stepped(state, deliver(message({ sender: 'phone-a', id }))).state;
  }
  const before = JSON.stringify(state.seen);
  // The redelivery of a held message is a duplicate, never a refusal that
  // would make the sender re-send, and never a second acceptance.
  assert.equal(stepped(state, deliver(message({ sender: 'phone-a', id: 'a-1' }))).outcome, OUTCOME.DUPLICATE);
  refused(state, deliver(message({ sender: 'phone-a', id: 'a-0', at: NOW - WINDOW - 1 })), REFUSAL.REPLAY);
  assert.equal(JSON.stringify(state.seen), before);

  let capped = newMailbox({ maxSeen: 2 });
  for (const id of ['m-0001', 'm-0002']) capped = stepped(capped, deliver(message({ id }))).state;
  assert.equal(stepped(capped, deliver(message({ id: 'm-0001' }))).outcome, OUTCOME.DUPLICATE);
  refused(capped, deliver(message({ id: 'm-0000', at: NOW - WINDOW - 1 })), REFUSAL.REPLAY);
  refused(capped, deliver(message({ id: 'm-0003' })), REFUSAL.DEDUPE_FULL);
  const muchLater = NOW + 100 * WINDOW;
  refused(capped, deliver(message({ id: 'm-0003', at: muchLater }), { now: muchLater }), REFUSAL.DEDUPE_FULL);
});

test('the ledger is read by index from a plain array, never iterated', () => {
  const state = newMailbox();
  const pending = { sender: 'phone-a', id: 'm-0001', at: NOW - 1_000, kind: KIND.COMMAND, status: STATUS.ACCEPTED };

  // An own iterator that yields nothing would hide a pending record: the
  // first round read this ledger as empty and lost the work.
  const iterating = [pending];
  iterating[Symbol.iterator] = function* () {};
  refused({ ...state, phase: STATE.PENDING, seen: iterating }, act(), REFUSAL.INVALID_SEEN);

  // An index accessor is refused, and never called.
  let reads = 0;
  const accessor = [];
  Object.defineProperty(accessor, '0', {
    get: () => {
      reads++;
      return pending;
    },
    enumerable: true,
    configurable: true,
  });
  refused({ ...state, phase: STATE.PENDING, seen: accessor }, act(), REFUSAL.INVALID_SEEN);
  assert.equal(reads, 0, 'the getter must never be called');

  const extra = [pending];
  extra.note = 'x';
  refused({ ...state, phase: STATE.PENDING, seen: extra }, act(), REFUSAL.INVALID_SEEN);

  const holey = [];
  holey.length = 1;
  refused({ ...state, seen: holey }, act(), REFUSAL.INVALID_SEEN);

  class Ledger extends Array {}
  refused({ ...state, phase: STATE.PENDING, seen: Ledger.from([pending]) }, act(), REFUSAL.INVALID_SEEN);

  // (A realm whose built-ins are themselves tampered with -- a replaced
  // Array.prototype[Symbol.iterator] -- is out of scope, as the README's
  // honest limits say: this module trusts its own realm.)
});

test('a refusal never echoes a key name the input chose', () => {
  // Built at run time, from harmless pieces.
  const keys = [
    ['message', '.SECRET=', 'tok123'].join(''),
    ['sk', '-ant-', 'xyz'].join(''),
  ];
  const state = newMailbox();
  for (const key of keys) {
    const entry = { sender: 'phone-a', id: 'm-0001', at: NOW - 1_000, kind: KIND.COMMAND, status: STATUS.ACKED, [key]: 1 };
    const results = [
      refused(state, deliver({ ...message(), [key]: 1 }), REFUSALS.UNKNOWN_FIELD),
      refused({ ...state, [key]: 1 }, act(), REFUSAL.UNKNOWN_FIELD),
      refused(state, { ...act(), [key]: 1 }, REFUSAL.UNKNOWN_FIELD),
      refused({ ...state, seen: [entry] }, act(), REFUSAL.INVALID_SEEN_ENTRY),
      initialState({ ...options(), [key]: 1 }),
    ];
    for (const result of results) {
      assert.equal(result.ok, false);
      assert.ok(!JSON.stringify(result).includes(key), `a refusal echoed the key: ${JSON.stringify(result)}`);
      assert.ok(!JSON.stringify(result).includes('SECRET') && !JSON.stringify(result).includes('ant'));
    }
  }
  // The fixed paths it names instead.
  assert.equal(refused(state, deliver({ ...message(), [keys[0]]: 1 })).field, 'message');
  assert.equal(refused({ ...state, [keys[0]]: 1 }, act()).field, 'state');
  assert.equal(refused(state, { ...act(), [keys[0]]: 1 }).field, 'event');
  assert.equal(initialState({ ...options(), [keys[0]]: 1 }).field, 'options');

  // A symbol key is a key like any other: refused, not skipped.
  refused({ ...state, [Symbol('extra')]: 1 }, act(), REFUSAL.UNKNOWN_FIELD);
  refused(state, { ...act(), [Symbol('extra')]: 1 }, REFUSAL.UNKNOWN_FIELD);
});

test('initialState answers with a frozen value, outside as well as in', () => {
  const created = initialState(options());
  assert.ok(Object.isFrozen(created), 'the outer { ok, state } must be frozen');
  assert.ok(Object.isFrozen(created.state));
  assert.ok(Object.isFrozen(initialState({})), 'a refusal is frozen too');
});

test('time is bounded by the lease\'s ceiling, and the skew is the lease\'s', () => {
  assert.equal(TIME_MAX, MAX_TIMESTAMP, 'the ceiling is imported, not restated');
  assert.equal(DEFAULT_FUTURE_TOLERANCE_MS, MAX_CLOCK_SKEW, 'the skew is imported, not restated');

  const state = newMailbox();
  refused(state, close(TIME_MAX + 1), REFUSAL.INVALID_NOW);
  refused({ ...state, now: TIME_MAX + 1 }, act(), REFUSAL.INVALID_NOW);

  // At the ceiling the mailbox still works, and an `at` past the ceiling is
  // refused even where the tolerance alone would admit it -- otherwise it
  // would be written into a ledger this module's own reader refuses.
  const top = newMailbox({ now: TIME_MAX });
  refused(top, deliver(message({ at: TIME_MAX + 1 }), { now: TIME_MAX }), REFUSAL.FUTURE_TIMESTAMP);
  const delivered = stepped(top, deliver(message({ at: TIME_MAX }), { now: TIME_MAX }));
  const acted = stepped(delivered.state, act(TIME_MAX));
  assert.equal(stepped(acted.state, ack(TIME_MAX)).phase, STATE.IDLE);
});

test('a future tolerance of zero is a tolerance, read back like any other', () => {
  const state = newMailbox({ futureToleranceMs: 0 });
  assert.equal(state.futureToleranceMs, 0);
  const now = stepped(state, deliver(message({ at: NOW })));
  assert.equal(now.outcome, OUTCOME.ACCEPTED);
  assert.equal(step(now.state, act()).ok, true, 'a state with tolerance 0 must be readable');
  refused(state, deliver(message({ at: NOW + 1 })), REFUSAL.FUTURE_TIMESTAMP);
});

// ---------------------------------------------------------------------------
// The per-kind window (owner ruling, 2026-10-17): commands keep the replay
// window; a prompt is accepted for its TTL and is `expired` after it
// ---------------------------------------------------------------------------

const prompt = (value, overrides = {}) => deliver(value, { kind: KIND.PROMPT, ...overrides });

test('a prompt older than the command window but inside its TTL is accepted; a command is a replay', () => {
  const age = WINDOW + 1;
  refused(newMailbox(), deliver(message({ at: NOW - age })), REFUSAL.REPLAY);
  const accepted = stepped(newMailbox(), prompt(message({ at: NOW - age })));
  assert.equal(accepted.outcome, OUTCOME.ACCEPTED);
  assert.equal(accepted.state.seen[0].kind, KIND.PROMPT);
  // And it is not stale when acted on: its window is its TTL.
  assert.equal(stepped(accepted.state, act()).outcome, OUTCOME.ACTED);
});

test('a prompt older than its TTL is expired, not a replay; at the TTL it is accepted', () => {
  const result = refused(newMailbox(), prompt(message({ at: NOW - PROMPT_TTL - 1 })), REFUSAL.EXPIRED);
  assert.equal(result.field, 'message.at');
  assert.equal(stepped(newMailbox(), prompt(message({ at: NOW - PROMPT_TTL }))).outcome, OUTCOME.ACCEPTED);
});

test('a delivery is a command unless it says otherwise, and the kind is a closed vocabulary', () => {
  const plain = stepped(newMailbox(), deliver(message()));
  assert.equal(plain.state.seen[0].kind, KIND.COMMAND);
  assert.equal(stepped(newMailbox(), deliver(message(), { kind: KIND.COMMAND })).state.seen[0].kind, KIND.COMMAND);
  for (const kind of ['reply', 'PROMPT', '', 1, null]) {
    refused(newMailbox(), deliver(message(), { kind }), REFUSAL.UNKNOWN_KIND);
  }
  // Only a delivery carries a kind.
  refused(newMailbox(), { ...act(), kind: KIND.PROMPT }, REFUSAL.UNKNOWN_FIELD);
});

test('the same pair and at under another kind is a conflict, not a duplicate', () => {
  const delivered = stepped(newMailbox(), deliver(message()));
  refused(delivered.state, prompt(message()), REFUSAL.ID_CONFLICT);
  const asPrompt = stepped(newMailbox(), prompt(message()));
  refused(asPrompt.state, deliver(message()), REFUSAL.ID_CONFLICT);
});

test('an acked prompt is never evicted before its TTL, and never re-admitted after', () => {
  let state = stepped(newMailbox({ maxSeen: 3 }), prompt(message({ id: 'p-1', at: NOW }))).state;
  state = stepped(state, act()).state;
  state = stepped(state, ack()).state;

  // Past the command window: a command record would go, the prompt's stays.
  const later = NOW + WINDOW + 1;
  state = stepped(state, deliver(message({ id: 'c-1', at: later }), { now: later })).state;
  assert.deepEqual(ids(state), ['p-1', 'c-1'], 'an acked prompt was evicted inside its TTL');
  assert.equal(stepped(state, prompt(message({ id: 'p-1', at: NOW }), { now: later })).outcome, OUTCOME.DUPLICATE);

  // At exactly the TTL's age it is still kept.
  const edge = NOW + PROMPT_TTL;
  state = stepped(state, deliver(message({ id: 'c-2', at: edge }), { now: edge })).state;
  assert.ok(ids(state).includes('p-1'), 'an acked prompt was evicted at age == TTL');

  // Past it, it goes, and its redelivery has expired: never re-admitted.
  const past = edge + 1;
  state = stepped(state, deliver(message({ id: 'c-3', at: past }), { now: past })).state;
  assert.ok(!ids(state).includes('p-1'));
  refused(state, prompt(message({ id: 'p-1', at: NOW }), { now: past }), REFUSAL.EXPIRED);
});

test('an accepted prompt past its TTL is stale-pending; between the window and the TTL it is not', () => {
  const state = stepped(newMailbox(), prompt(message({ at: NOW }))).state;
  assert.equal(stepped(state, act(NOW + WINDOW + 1)).outcome, OUTCOME.ACTED);
  assert.equal(stepped(state, act(NOW + PROMPT_TTL)).outcome, OUTCOME.ACTED);
  assert.equal(stepped(state, act(NOW + PROMPT_TTL + 1)).outcome, OUTCOME.STALE_PENDING);
});

test('the per-sender quota counts prompts and commands together', () => {
  let state = newMailbox({ maxSeen: 8, maxPerSender: 2 });
  state = stepped(state, deliver(message({ id: 'c-1' }))).state;
  state = stepped(state, prompt(message({ id: 'p-1' }))).state;
  refused(state, prompt(message({ id: 'p-2' })), REFUSAL.SENDER_FULL);
  refused(state, deliver(message({ id: 'c-2' })), REFUSAL.SENDER_FULL);
});

// ---------------------------------------------------------------------------
// Round 3 (second independent review): eviction cannot depend on a claim the
// event makes, nor on a window a later state widens; acked records cannot
// lock the ledger; `now` cannot leap; nothing throws
// ---------------------------------------------------------------------------

const HOUR = 60 * 60 * 1_000;

/** A mailbox with real-sized windows, for the reviewer's probes. */
function sized(overrides = {}) {
  return newMailbox({ windowMs: 6 * HOUR, promptTtlMs: 72 * HOUR, maxSeen: 8, ...overrides });
}

test('probe A: a command evicted at 7h cannot come back by claiming to be a prompt', () => {
  let state = stepped(sized(), deliver(message({ id: 'x', at: NOW }))).state;
  state = stepped(state, act()).state;
  state = stepped(state, ack()).state;
  const t = NOW + 7 * HOUR;
  state = stepped(state, deliver(message({ id: 'y', at: t }), { now: t })).state;

  const back = step(state, deliver(message({ id: 'x', at: NOW }), { now: t, kind: KIND.PROMPT }));
  assert.equal(back.ok, false, `x was re-admitted as a prompt: ${JSON.stringify(back)}`);
});

test('probe C: a prompt expired and evicted cannot come back as a command', () => {
  let state = sized({ promptTtlMs: HOUR });
  state = stepped(state, deliver(message({ id: 'x', at: NOW }), { kind: KIND.PROMPT })).state;
  state = stepped(state, act()).state;
  state = stepped(state, ack()).state;
  const t = NOW + 2 * HOUR;
  state = stepped(state, deliver(message({ id: 'y', at: t }), { now: t })).state;

  const back = step(state, deliver(message({ id: 'x', at: NOW }), { now: t }));
  assert.equal(back.ok, false, `x was re-admitted as a command: ${JSON.stringify(back)}`);
});

test('probe D: widening a window on a stored state does not let an evicted message back in', () => {
  let state = stepped(sized({ promptTtlMs: 6 * HOUR }), deliver(message({ id: 'x', at: NOW }))).state;
  state = stepped(state, act()).state;
  state = stepped(state, ack()).state;
  const t = NOW + 6 * HOUR + 1;
  state = stepped(state, deliver(message({ id: 'y', at: t }), { now: t })).state;
  assert.ok(!ids(state).includes('x'), 'x should have been evicted');

  const widened = { ...state, windowMs: 12 * HOUR, promptTtlMs: 12 * HOUR };
  const later = NOW + 7 * HOUR;
  const back = step(widened, deliver(message({ id: 'x', at: NOW }), { now: later }));
  assert.equal(back.ok, false, `x was re-admitted after the window was widened: ${JSON.stringify(back)}`);
});

test('probe F, as ruled: many senders can fill the ledger; the refusal is dedupe-full, and the horizon recovers', () => {
  // Four senders at their quota of 2, all acked, fill an 8-slot ledger inside
  // a 72h prompt TTL. A fifth is refused -- the lockout trade-off the README
  // documents (it needs `sender` tied to the writer by P2.1's rules) -- and is
  // admitted once the records age past the horizon. Nothing is compacted.
  let state = sized({ maxPerSender: 2 });
  for (const sender of ['s1', 's2', 's3', 's4']) {
    for (const id of ['p-1', 'p-2']) {
      state = stepped(state, deliver(message({ sender, id }), { kind: KIND.PROMPT })).state;
      state = stepped(state, act()).state;
      state = stepped(state, ack()).state;
    }
  }
  assert.equal(state.seen.length, 8);
  const t = NOW + 71 * HOUR;
  refused(state, deliver(message({ sender: 's5', id: 'p-1', at: t }), { now: t, kind: KIND.PROMPT }), REFUSAL.DEDUPE_FULL);
  const later = NOW + 72 * HOUR + 1_001;
  const fifth = stepped(state, deliver(message({ sender: 's5', id: 'p-1', at: later }), { now: later, kind: KIND.PROMPT }));
  assert.equal(fifth.outcome, OUTCOME.ACCEPTED);
  assert.deepEqual(ids(fifth.state), ['p-1']);
});

test('probe E: one far-future now cannot freeze the mailbox', () => {
  const state = newMailbox();
  const leap = refused(state, close(TIME_MAX), REFUSAL.NOW_JUMPED);
  assert.equal(leap.field, 'now');
  // A jump of exactly the maximum is accepted; one millisecond more is not.
  assert.equal(step(state, act(NOW + MAX_FORWARD_JUMP_MS)).ok, true);
  refused(state, act(NOW + MAX_FORWARD_JUMP_MS + 1), REFUSAL.NOW_JUMPED);
});

test('a throwing proxy is a refusal, never an exception, and the refusal echoes nothing', () => {
  const secret = ['gh', 'p_', 'B'.repeat(36)].join('');
  const { proxy: revokedState, revoke } = Proxy.revocable({}, {});
  revoke();
  const trap = new Proxy({}, { ownKeys() { throw new Error(secret); } });
  const ledgerTrap = new Proxy([], { ownKeys() { throw new Error(secret); } });
  const results = [
    step(revokedState, act()),
    step(newMailbox(), trap),
    step(trap, act()),
    step({ ...newMailbox(), seen: ledgerTrap }, act()),
    initialState(trap),
  ];
  for (const result of results) {
    assert.equal(result.ok, false);
    assert.ok(Object.isFrozen(result));
    assert.ok(!JSON.stringify(result).includes(secret), JSON.stringify(result));
  }
  assert.ok(results.some((result) => result.code === REFUSAL.INTERNAL_ERROR));
});

test('the watermark is an integer from 0, set only by the module', () => {
  const state = newMailbox();
  assert.equal(state.evictedThrough, 0);
  for (const bad of [-1, -0, 1.5, '0', null]) {
    refused({ ...state, evictedThrough: bad }, act(), REFUSAL.INVALID_EVICTED_THROUGH);
  }
  const missing = { ...state };
  delete missing.evictedThrough;
  refused(missing, act(), REFUSAL.MISSING_FIELD);
  // It is the module's to set, not the caller's to choose at the start.
  assert.equal(initialState({ ...options(), evictedThrough: 5 }).code, REFUSAL.UNKNOWN_FIELD);
});


test('the horizon covers the command window even when the prompt TTL is shorter', () => {
  // promptTtlMs (1h) < windowMs (6h): an acked command at 2h is still inside
  // its window, so its record must still be there and its redelivery is a
  // duplicate -- not a replay refused by a watermark it should never have
  // raised.
  let state = sized({ promptTtlMs: HOUR });
  state = stepped(state, deliver(message({ id: 'x', at: NOW }))).state;
  state = stepped(state, act()).state;
  state = stepped(state, ack()).state;
  const t = NOW + 2 * HOUR;
  state = stepped(state, deliver(message({ id: 'y', at: t }), { now: t })).state;
  assert.deepEqual(ids(state), ['x', 'y']);
  assert.equal(state.evictedThrough, 0);
  assert.equal(stepped(state, deliver(message({ id: 'x', at: NOW }), { now: t })).outcome, OUTCOME.DUPLICATE);
});

test('a prompt at or below the watermark has expired; a command there is a replay', () => {
  // horizon = WINDOW (promptTtlMs = WINDOW), watermark at its latest legal
  // value: a message aged exactly the window passes the window rule (it is
  // inclusive), and the watermark is what refuses it.
  const state = { ...newMailbox({ promptTtlMs: WINDOW }), evictedThrough: NOW - WINDOW };
  refused(state, deliver(message({ at: NOW - WINDOW }), { kind: KIND.PROMPT }), REFUSAL.EXPIRED);
  refused(state, deliver(message({ at: NOW - WINDOW })), REFUSAL.REPLAY);
  assert.equal(stepped(state, deliver(message({ at: NOW - WINDOW + 1 }))).outcome, OUTCOME.ACCEPTED);
});

// ---------------------------------------------------------------------------
// Round 4 (third review; design ruled by the coordinator): no compaction
// inside the horizon; the watermark moves only by horizon age; the quota
// counts every record a sender holds
// ---------------------------------------------------------------------------

test('round-3 HIGH: a future-dated acked record never pushes the watermark past now', () => {
  // An acked record dated inside the future tolerance, then the ledger fills.
  let state = newMailbox({ maxSeen: 2 });
  state = stepped(state, deliver(message({ id: 'f', at: NOW + TOLERANCE }))).state;
  state = stepped(state, act()).state;
  state = stepped(state, ack()).state;
  state = stepped(state, deliver(message({ id: 'y' }))).state;
  const full = step(state, deliver(message({ id: 'z' })));
  if (full.ok) state = full.state;
  // Whatever the answer, every later step must still read the state.
  assert.ok(state.evictedThrough <= state.now, `watermark ${state.evictedThrough} > now ${state.now}`);
  assert.equal(step(state, act(NOW + 1)).ok, true, 'the mailbox is stuck');
  assert.equal(full.ok, false, 'a full ledger must refuse, not compact');
  assert.equal(full.code, REFUSAL.DEDUPE_FULL);
});

test('round-3 MEDIUM 1: one sender\'s flood cannot censor another sender', () => {
  const T0 = NOW;
  let state = sized({ maxPerSender: undefined }); // the default quota: 8 / 4 = 2
  const flood = T0 + HOUR;
  let refusedFlood = 0;
  for (let i = 0; i < 9; i++) {
    const delivered = step(state, deliver(message({ sender: 'evil', id: `e-${i}`, at: flood }), { now: flood }));
    if (!delivered.ok) {
      assert.equal(delivered.code, REFUSAL.SENDER_FULL);
      refusedFlood++;
      continue;
    }
    state = stepped(delivered.state, act(flood)).state;
    state = stepped(state, ack(flood)).state;
  }
  assert.equal(refusedFlood, 7, 'the flooder is held to its own quota');
  assert.equal(state.evictedThrough, 0, 'a flood moved the watermark');
  const bob = stepped(state, deliver(message({ sender: 'bob', id: 'b-1', at: T0 + 10_000 }), { now: flood, kind: KIND.PROMPT }));
  assert.equal(bob.outcome, OUTCOME.ACCEPTED);
});

test('round-3 MEDIUM 2: inside the horizon the same id is never acted on twice, even at maxSeen 1', () => {
  let state = stepped(newMailbox({ maxSeen: 1 }), deliver(message({ id: 'x', at: NOW }))).state;
  state = stepped(state, act()).state;
  state = stepped(state, ack()).state;
  const again = step(state, deliver(message({ id: 'x', at: NOW + 6 }), { now: NOW + 6 }));
  assert.equal(again.ok, false, `x was re-admitted: ${JSON.stringify(again)}`);
  assert.equal(again.code, REFUSAL.ID_CONFLICT);
  refused(state, deliver(message({ id: 'y', at: NOW + 6 }), { now: NOW + 6 }), REFUSAL.DEDUPE_FULL);
  assert.equal(stepped(state, deliver(message({ id: 'x', at: NOW }), { now: NOW + 6 })).outcome, OUTCOME.DUPLICATE);
});

test('the watermark never passes now minus the horizon', () => {
  const state = newMailbox(); // horizon = max(WINDOW, PROMPT_TTL) = PROMPT_TTL
  assert.equal(step({ ...state, evictedThrough: NOW - PROMPT_TTL }, act()).ok, true);
  refused({ ...state, evictedThrough: NOW - PROMPT_TTL + 1 }, act(), REFUSAL.INVALID_EVICTED_THROUGH);
  // Early in time, when now is within one horizon of the epoch, only 0 fits.
  const early = newMailbox({ now: 5 });
  refused({ ...early, evictedThrough: 1 }, act(5), REFUSAL.INVALID_EVICTED_THROUGH);
  assert.equal(step(early, act(5)).ok, true);
});

test('the per-sender quota counts every record a sender holds inside the horizon, acked or not', () => {
  let state = newMailbox({ maxSeen: 8, maxPerSender: 2 });
  for (const id of ['a-1', 'a-2']) {
    state = stepped(state, deliver(message({ id }))).state;
    state = stepped(state, act()).state;
    state = stepped(state, ack()).state;
  }
  refused(state, deliver(message({ id: 'a-3' })), REFUSAL.SENDER_FULL);
  // Once the records age past the horizon they are evicted, and A has room.
  const past = NOW + PROMPT_TTL + 1_001;
  assert.equal(stepped(state, deliver(message({ id: 'a-3', at: past }), { now: past })).outcome, OUTCOME.ACCEPTED);
});
