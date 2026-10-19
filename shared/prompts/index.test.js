// Tests for the prompt queue and the interrupt policy (mobile plan, Task P6.5).
//
// The tests are organised by the plan's own verification line: prompts queue by
// default; a natural pause announces the count across accounts; "what's
// pending" lists them per installation; an unanswered prompt expires at D118's
// mailbox TTL as a pure function of its `at`, the TTL and the consumer's clock
// and yields its non-destructive answer or a stop; an answer is accepted within
// its TTL except an answer to a destructive prompt, which takes the command
// window (D173); reminder escalation; a hard cap per consumer; only D160's set
// interrupts, narrowed or widened by D127's local switch; and the whole thing a
// pure function of (queue, digest, presence, switch, now).
//
// Two of these tests reach into other modules on purpose, because the value
// being tested is an AGREEMENT and not an arithmetic: the expiry boundary is
// asserted against P1.1's own `step` (a prompt of age exactly the TTL is still
// accepted there), and the reminder schedule against the clock rather than
// against a copy of the schedule.

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
  initialState,
  KIND as MAILBOX_KIND,
  MAX_FORWARD_JUMP_MS,
  MAX_FUTURE_TOLERANCE_MS,
  step,
  TIME_MAX,
} from '../mailbox/index.js';
import { validateMessage } from '../mailbox-schema/index.js';
import { SEVERITY } from '../digest/severity.js';
import { STATE as PRESENCE } from '../presence/index.js';
import {
  announcement,
  acceptanceKind,
  announce,
  answer,
  DEFAULT_MAX_PENDING,
  enqueue,
  evaluateInterrupt,
  expire,
  EXPIRED_REPLY,
  initialQueue,
  INTERRUPT,
  isExpired,
  MAX_JUMP_MS,
  MAX_TTL_MS,
  MAX_MAX_PENDING,
  MIN_TTL_MS,
  OUTCOME,
  pending,
  pendingAcross,
  PROMPT_TTL_MS,
  QUEUE_FULL_REPLY,
  REFUSAL,
  REMINDER_AFTER_MS,
  REMINDER_INTERVAL_MS,
  reminderStage,
  SWITCH,
  waitingSentence,
} from './index.js';

const T0 = 1_700_000_000_000;
const MINUTE = 60 * 1_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/** A well-formed `mac`: 32 bytes of hex, which is all P6.3's schema asks for. */
const MAC = 'a'.repeat(64);

function promptMessage(over = {}) {
  return {
    id: 'p1',
    sender: 'agent-1',
    text: 'Deploy the release?',
    at: T0,
    mac: MAC,
    kind: 'prompt',
    ...over,
  };
}

function answerMessage(over = {}) {
  return {
    id: 'a1',
    sender: 'phone',
    text: 'yes',
    at: T0,
    mac: MAC,
    kind: 'answer',
    reply_to: 'p1',
    ...over,
  };
}

/** D169's action record, as P6.3 validates it: a verb, a resolved target, an
 * action id, and the confirmation it was read back under. */
function actionRecord(at) {
  return {
    id: 'act-1',
    verb: 'cancel',
    target: { run: 'run-1' },
    confirmation: { id: 'cf-1', at },
  };
}

/** The phone's confirmation of a destructive prompt: kind `action`, per §3.5. */
function actionMessage(over = {}) {
  return {
    id: 'c1',
    sender: 'phone',
    // `text` is required on every P6.3 message, an action record included: the
    // record is what runs, and this is what was said.
    text: 'confirm',
    at: T0,
    mac: MAC,
    kind: 'action',
    reply_to: 'p1',
    action: actionRecord(T0),
    ...over,
  };
}

/** A queue for one installation, with the TTL pinned so the arithmetic is readable. */
function makeQueue(over = {}) {
  const built = initialQueue({ now: T0, account: 'acct-a', ...over });
  assert.equal(built.ok, true, `initialQueue refused: ${JSON.stringify(built)}`);
  return built.queue;
}

function enqueueOne(queue, over = {}, event = {}) {
  const result = enqueue(queue, { now: queue.now, prompt: promptMessage(over), ...event });
  return result;
}

// ---------------------------------------------------------------------------
// The constructor
// ---------------------------------------------------------------------------

test('a new queue is empty, and its defaults are the owner rulings', () => {
  const queue = makeQueue();
  assert.equal(queue.account, 'acct-a');
  assert.equal(queue.now, T0);
  assert.equal(queue.promptTtlMs, PROMPT_TTL_MS);
  // D118's mailbox TTL, as the owner set it on 2026-10-17: 72 hours, inside
  // P1.1's 30-day ceiling. The number is checked, not the expression, because
  // the number is the ruling.
  assert.equal(PROMPT_TTL_MS, 72 * 60 * 60 * 1_000);
  assert.ok(PROMPT_TTL_MS <= MAX_TTL_MS);
  assert.equal(queue.prompts.length, 0);
  assert.equal(queue.maxPending, DEFAULT_MAX_PENDING);
  // Frozen at every level: a queue is a value, and a caller that mutates one
  // is a caller whose next call this module would refuse.
  assert.ok(Object.isFrozen(queue));
  assert.ok(Object.isFrozen(queue.prompts));
  // The RESULT is frozen too, like every other object an exported entry point
  // hands back: this one was the exception, and a caller that could rewrite
  // `ok` or swap the queue on the result it was given would be a caller whose
  // next call this module could not predict.
  const built = initialQueue({ now: T0, account: 'acct-a' });
  assert.equal(built.ok, true);
  assert.ok(Object.isFrozen(built), 'the constructor result is frozen like every other');
  assert.ok(Object.isFrozen(built.queue));
});

test('a queue refuses what it cannot read, one code per fault', () => {
  const cases = [
    [{ now: T0, account: 'a', promptTtlMs: 1 }, REFUSAL.INVALID_TTL, 'promptTtlMs'],
    [{ now: T0, account: 'a', promptTtlMs: MAX_TTL_MS + 1 }, REFUSAL.INVALID_TTL, 'promptTtlMs'],
    [{ now: T0, account: 'a', windowMs: 0 }, REFUSAL.INVALID_WINDOW, 'windowMs'],
    [{ now: T0, account: 'a', maxPending: 0 }, REFUSAL.INVALID_MAX_PENDING, 'maxPending'],
    [{ now: T0, account: 'a', maxPending: MAX_MAX_PENDING + 1 }, REFUSAL.INVALID_MAX_PENDING, 'maxPending'],
    [{ now: T0, account: 'a', futureToleranceMs: -1 }, REFUSAL.INVALID_FUTURE_TOLERANCE, 'futureToleranceMs'],
    // P1.1's ceiling for the same field, imported rather than restated, and
    // checked at the boundary: this is how far a prompt's `at` may lead the
    // clock, and the reminder schedule is read off that clock.
    [
      { now: T0, account: 'a', futureToleranceMs: MAX_FUTURE_TOLERANCE_MS + 1 },
      REFUSAL.INVALID_FUTURE_TOLERANCE,
      'futureToleranceMs',
    ],
    [{ now: T0, account: '' }, REFUSAL.INVALID_ACCOUNT, 'account'],
    [{ now: T0, account: 7 }, REFUSAL.INVALID_ACCOUNT, 'account'],
    [{ now: 0, account: 'a' }, REFUSAL.INVALID_NOW, 'now'],
    [{ now: T0, account: 'a', prompts: [] }, REFUSAL.UNKNOWN_FIELD, 'options'],
    // A field that is not carried at all is not an unknown field: the two are
    // different faults with different fixes, and P1.1 and P6.2 both draw the
    // line here (`MISSING_FIELD`, spelled the same in all three).
    [{ account: 'a' }, REFUSAL.MISSING_FIELD, 'now'],
    [{ now: T0 }, REFUSAL.MISSING_FIELD, 'account'],
  ];
  for (const [options, code, field] of cases) {
    const result = initialQueue(options);
    assert.equal(result.ok, false, `expected a refusal for ${JSON.stringify(options)}`);
    assert.equal(result.code, code);
    assert.equal(result.field, field);
  }
  // A queue is not a bag: an unknown key is refused rather than dropped, and a
  // value that is not an object at all is refused as such.
  assert.equal(initialQueue({ now: T0, account: 'a', ttl: 1 }).code, REFUSAL.UNKNOWN_FIELD);
  assert.equal(initialQueue(null).code, REFUSAL.INPUT_NOT_AN_OBJECT);
  assert.equal(initialQueue([]).code, REFUSAL.INPUT_NOT_AN_OBJECT);
  assert.equal(initialQueue().code, REFUSAL.MISSING_FIELD);
  assert.equal(initialQueue().field, 'now', 'the first field a queue needs is the clock');
});

test('a queue is not a bag: an accessor is refused without being called', () => {
  let called = 0;
  const options = { now: T0, account: 'a' };
  Object.defineProperty(options, 'maxPending', {
    enumerable: true,
    get() {
      called += 1;
      return 4;
    },
  });
  const result = initialQueue(options);
  assert.equal(result.ok, false);
  assert.equal(called, 0, 'the getter must not be called: a value read twice is not a value');
  // The code is its OWN, and not `unknown-field`: `maxPending` is a field this
  // module names and knows, and refusing it as an unknown key would tell the
  // caller to fix the wrong thing. P1.1 draws the same line for the same reason
  // (`shared/mailbox/index.js`'s ACCESSOR_PROPERTY), and the codes are spelled
  // the same so a caller can branch on either module's answer.
  assert.equal(result.code, REFUSAL.ACCESSOR_PROPERTY);
  assert.equal(result.field, 'maxPending');
  assert.equal(REFUSAL.ACCESSOR_PROPERTY, 'accessor-property');
});

// ---------------------------------------------------------------------------
// Expiry: a pure function of `at`, the TTL and `now` (D168 as amended)
// ---------------------------------------------------------------------------

test('isExpired is strictly-greater, and agrees with P1.1 at the boundary', () => {
  const ttl = 6 * HOUR;
  assert.equal(isExpired(T0, T0, ttl), false);
  assert.equal(isExpired(T0, T0 + ttl - 1, ttl), false);
  assert.equal(isExpired(T0, T0 + ttl, ttl), false, 'age exactly the TTL is still alive');
  assert.equal(isExpired(T0, T0 + ttl + 1, ttl), true);

  // The agreement that matters, asserted against P1.1's own step rather than
  // against the same arithmetic written twice: the instant this module calls a
  // prompt dead is the instant P1.1 stops accepting it, so an expiry and an
  // accepted answer can never disagree about whether a prompt is alive.
  const built = initialState({ now: T0, promptTtlMs: ttl });
  assert.equal(built.ok, true);
  for (const [age, alive] of [
    [ttl - 1, true],
    [ttl, true],
    [ttl + 1, false],
    [ttl + DAY, false],
  ]) {
    const delivered = step(built.state, {
      type: 'deliver',
      now: T0 + age,
      kind: MAILBOX_KIND.PROMPT,
      message: promptMessage({ at: T0 }),
    });
    assert.equal(
      isExpired(T0, T0 + age, ttl),
      !delivered.ok,
      `P1.1 and P6.5 disagree at age ${age}: ${JSON.stringify(delivered)}`,
    );
    if (!delivered.ok) {
      assert.equal(delivered.code, 'expired');
      assert.equal(alive, false);
    }
  }
});

test('isExpired refuses an argument it cannot read, rather than answering false', () => {
  // The README and the docstring both promise this, and the promise is the
  // reason the predicate is guarded at all: `false` means "still alive", so a
  // predicate that cannot run must not give it -- the user would be handed a
  // question whose TTL has ended. Coercion gives exactly that answer
  // (`now - at > ttl` is false for `undefined`, `'x'` and `NaN`, and TRUE for
  // `null`, which is not a number either), so each of the three arguments is
  // checked as the argument it is, and the refusal names the one at fault with
  // the code this module already uses for that argument elsewhere.
  const ttl = 6 * HOUR;
  const cases = [
    [undefined, T0, ttl, REFUSAL.INVALID_AT, 'at'],
    [null, T0, ttl, REFUSAL.INVALID_AT, 'at'],
    ['x', T0, ttl, REFUSAL.INVALID_AT, 'at'],
    [NaN, T0, ttl, REFUSAL.INVALID_AT, 'at'],
    [-0, T0, ttl, REFUSAL.INVALID_AT, 'at'],
    [0, T0, ttl, REFUSAL.INVALID_AT, 'at'],
    [T0 + 0.5, T0, ttl, REFUSAL.INVALID_AT, 'at'],
    [T0, undefined, ttl, REFUSAL.INVALID_NOW, 'now'],
    [T0, null, ttl, REFUSAL.INVALID_NOW, 'now'],
    [T0, '', ttl, REFUSAL.INVALID_NOW, 'now'],
    [T0, 0, ttl, REFUSAL.INVALID_NOW, 'now'],
    [T0, T0, undefined, REFUSAL.INVALID_TTL, 'promptTtlMs'],
    [T0, T0, null, REFUSAL.INVALID_TTL, 'promptTtlMs'],
    [T0, T0, 'x', REFUSAL.INVALID_TTL, 'promptTtlMs'],
    [T0, T0, 0, REFUSAL.INVALID_TTL, 'promptTtlMs'],
    [T0, T0, MAX_TTL_MS + 1, REFUSAL.INVALID_TTL, 'promptTtlMs'],
  ];
  for (const [at, now, given, code, field] of cases) {
    const result = isExpired(at, now, given);
    const what = `isExpired(${String(at)}, ${String(now)}, ${String(given)})`;
    assert.equal(result.ok, false, `${what} must refuse, not answer ${String(result)}`);
    assert.equal(result.code, code, what);
    assert.equal(result.field, field, what);
    assert.ok(Object.isFrozen(result), what);
  }
  // A hostile value in a position one of the three arguments belongs in is
  // refused like any other value that is not a number: the check is a type test,
  // so it answers rather than throwing, and the answer is never `false`.
  assert.equal(isExpired(hostile(), T0, ttl).code, REFUSAL.INVALID_AT);
  assert.equal(isExpired(T0, revoked(), ttl).code, REFUSAL.INVALID_NOW);
  assert.equal(isExpired(T0, T0, hostile()).code, REFUSAL.INVALID_TTL);
  // And the arguments that ARE arguments still answer the rule, including at
  // the shortest TTL the module accepts.
  assert.equal(isExpired(T0, T0 + ttl + 1, ttl), true);
  assert.equal(isExpired(T0, T0 + MIN_TTL_MS + 1, MIN_TTL_MS), true);
});

// ---------------------------------------------------------------------------
// Queueing by default
// ---------------------------------------------------------------------------

test('a prompt queues by default, and in canonical order', () => {
  let queue = makeQueue();
  // Delivered out of order on purpose: the queue's order is `at`, then the id,
  // so two consumers that received the same prompts in different orders hold
  // the same queue.
  for (const message of [
    promptMessage({ id: 'p3', at: T0 + 3_000 }),
    promptMessage({ id: 'p1', at: T0 + 1_000 }),
    promptMessage({ id: 'p2', at: T0 + 2_000 }),
  ]) {
    const result = enqueue(queue, { now: T0, prompt: message });
    assert.equal(result.ok, true, JSON.stringify(result));
    assert.equal(result.outcome, OUTCOME.QUEUED);
    queue = result.queue;
  }
  assert.deepEqual(queue.prompts.map((entry) => entry.prompt.id), ['p1', 'p2', 'p3']);
  // Two prompts asked in the same millisecond are ordered by id, so the order
  // is total: no two deliveries can leave two different queues.
  const second = makeQueue();
  let left = enqueue(second, { now: T0, prompt: promptMessage({ id: 'b', at: T0 }) }).queue;
  left = enqueue(left, { now: T0, prompt: promptMessage({ id: 'a', at: T0 }) }).queue;
  let right = enqueue(second, { now: T0, prompt: promptMessage({ id: 'a', at: T0 }) }).queue;
  right = enqueue(right, { now: T0, prompt: promptMessage({ id: 'b', at: T0 }) }).queue;
  assert.deepEqual(left, right);
});

test('a queued prompt keeps its local classification beside the message, not in it', () => {
  const result = enqueue(makeQueue(), {
    now: T0,
    prompt: promptMessage({ options: ['ship it', 'hold'] }),
    destructive: true,
    safe: 'hold',
  });
  assert.equal(result.ok, true);
  const entry = result.queue.prompts[0];
  assert.equal(entry.destructive, true);
  assert.equal(entry.safe, 'hold');
  assert.equal(entry.announcedAt, null);
  // D127's structural guarantee: the classification is not in the message, and
  // the schema is what makes that true rather than a convention -- a message
  // carrying a field the schema does not name is refused, so nothing local can
  // reach the bytes the MAC covers. Asserted through the validator itself.
  assert.equal(validateMessage({ ...promptMessage(), destructive: true }).ok, false);
  assert.equal(validateMessage({ ...promptMessage(), safe: 'hold' }).ok, false);
  // The message the queue holds is the schema's own frozen copy.
  assert.ok(Object.isFrozen(entry.prompt));
  assert.equal(entry.prompt.kind, 'prompt');
});

test('enqueue refuses what it cannot read, with the schema\'s own codes', () => {
  const queue = makeQueue();
  const now = T0;

  assert.equal(enqueue(queue, { now, prompt: promptMessage({ kind: 'message' }) }).code, REFUSAL.NOT_A_PROMPT);
  assert.equal(enqueue(queue, { now, prompt: promptMessage({ text: '' }) }).code, 'empty-string');
  const unknown = enqueue(queue, { now, prompt: promptMessage({ nonsense: true }) });
  assert.equal(unknown.code, 'unknown-field');
  // The schema names an unknown field by its KEY, and that key is the writer's:
  // it is not a name this module knows, so it is not echoed back -- whatever it
  // holds (a key's name, a path) would then travel out of this module in an
  // answer it never read. A field the schema does define IS named. P1.1's rule,
  // spelled the same way and imported from the same list (`FIELDS`).
  assert.equal(unknown.field, 'prompt');
  const unreadable = promptMessage();
  delete unreadable.mac;
  const schemaField = enqueue(queue, { now, prompt: unreadable });
  assert.equal(schemaField.code, 'missing-field');
  assert.equal(schemaField.field, 'prompt.mac', 'a field the schema defines is named, prefixed');
  // The schema names the field at fault, and the code is the schema's: this
  // module does not re-decide what a prompt is.
  assert.equal(
    enqueue(queue, { now, prompt: promptMessage({ at: 1.5 }) }).code,
    'bad-timestamp',
  );
  // A queued prompt with no options cannot answer with one.
  assert.equal(
    enqueue(queue, { now, prompt: promptMessage({ options: ['a', 'b'] }), safe: 'c' }).code,
    REFUSAL.SAFE_NOT_AN_OPTION,
  );
  assert.equal(
    enqueue(queue, { now, prompt: promptMessage({ options: ['a', 'b'] }), safe: '' }).code,
    REFUSAL.INVALID_SAFE,
  );
  assert.equal(
    enqueue(queue, { now, prompt: promptMessage({ at: T0 - 1, destructive: 'yes' }) }).ok,
    false,
  );
  assert.equal(
    enqueue(queue, { now, prompt: promptMessage(), destructive: 'yes' }).code,
    REFUSAL.INVALID_DESTRUCTIVE,
  );
  assert.equal(
    enqueue(queue, { now, prompt: promptMessage({ at: 0 }) }).code,
    REFUSAL.ZERO_TIMESTAMP,
  );
  // A negated zero is the residue of a computation, not a time: the schema
  // accepts it (it is numerically zero) and this module does not carry it.
  assert.equal(
    enqueue(queue, { now, prompt: promptMessage({ at: -0 }) }).code,
    REFUSAL.NEGATIVE_ZERO,
  );
  assert.equal(
    enqueue(queue, { now, prompt: promptMessage({ at: T0 + 10 * DAY }) }).code,
    REFUSAL.FUTURE_TIMESTAMP,
  );
  assert.equal(enqueue(queue, { now: T0 - 1, prompt: promptMessage() }).code, REFUSAL.NOW_WENT_BACKWARDS);
  assert.equal(enqueue(queue, { now, prompt: promptMessage(), extra: 1 }).code, REFUSAL.UNKNOWN_FIELD);
  assert.equal(enqueue(queue, { prompt: promptMessage() }).code, REFUSAL.MISSING_FIELD);
  assert.equal(enqueue(queue, { prompt: promptMessage() }).field, 'now');
  // Every operation that needs the clock says so the same way, because they all
  // open the queue the same way.
  assert.equal(expire(queue, {}).code, REFUSAL.MISSING_FIELD);
  assert.equal(pending(queue, {}).code, REFUSAL.MISSING_FIELD);
  assert.equal(announce(queue, {}).code, REFUSAL.MISSING_FIELD);
});

test('the same id under another sender or time is a conflict, not a duplicate', () => {
  const queue = enqueueOne(makeQueue()).queue;
  const again = enqueue(queue, { now: T0, prompt: promptMessage() });
  assert.equal(again.ok, true);
  assert.equal(again.outcome, OUTCOME.DUPLICATE);
  assert.deepEqual(again.queue, queue, 'a duplicate leaves the queue exactly as it was');
  assert.equal(
    enqueue(queue, { now: T0, prompt: promptMessage({ sender: 'agent-2' }) }).code,
    REFUSAL.ID_CONFLICT,
  );
  assert.equal(
    enqueue(queue, { now: T0, prompt: promptMessage({ at: T0 + 1_000 }) }).code,
    REFUSAL.ID_CONFLICT,
  );
});

test('a redelivery does not re-arm an announcement or reset a clock', () => {
  const first = enqueueOne(makeQueue());
  const spoken = announce(first.queue, { now: T0 + 2 * MINUTE });
  assert.equal(spoken.spoken.length, 1);
  const redelivered = enqueue(spoken.queue, { now: T0 + 3 * MINUTE, prompt: promptMessage() });
  assert.equal(redelivered.outcome, OUTCOME.DUPLICATE);
  assert.equal(
    redelivered.queue.prompts[0].announcedAt,
    T0 + 2 * MINUTE,
    'the announcement stands: the user has heard this question',
  );
});

test('the hard cap refuses the prompt past it, with a defined answer', () => {
  let queue = makeQueue({ maxPending: 2 });
  for (const id of ['p1', 'p2']) {
    const result = enqueue(queue, { now: T0, prompt: promptMessage({ id }) });
    assert.equal(result.ok, true, `the cap of 2 must hold ${id}`);
    queue = result.queue;
  }
  const refused = enqueue(queue, { now: T0, prompt: promptMessage({ id: 'p3' }) });
  assert.equal(refused.ok, false);
  assert.equal(refused.code, REFUSAL.QUEUE_FULL);
  assert.equal(refused.reply, QUEUE_FULL_REPLY);
  assert.equal(refused.maxPending, 2);
  // Refused, never evicted: the two held prompts are both still there, because
  // dropping one to make room would answer nothing and lose the dropped one.
  const listed = pending(queue, { now: T0 });
  assert.equal(listed.pending.length, 2);
});

test('a prompt that arrives already expired yields its answer, or a stop', () => {
  const queue = makeQueue();
  const late = enqueue(queue, {
    now: T0 + PROMPT_TTL_MS + 1,
    prompt: promptMessage({ at: T0, options: ['ship it', 'hold'] }),
    safe: 'hold',
  });
  assert.equal(late.ok, true, 'an expiry is an outcome, not a refusal');
  assert.equal(late.outcome, OUTCOME.EXPIRED);
  assert.equal(late.queue.prompts.length, 0, 'a dead prompt is not held');
  assert.equal(late.expiry.safe, 'hold');
  assert.equal(late.expiry.stop, false);
  assert.equal(late.expiry.reply, 'hold');

  // No non-destructive answer: the conversation stops, and it stops knowing
  // why (D168's "no answer, expired").
  const stopped = enqueue(queue, { now: T0 + PROMPT_TTL_MS + 1, prompt: promptMessage() });
  assert.equal(stopped.outcome, OUTCOME.EXPIRED);
  assert.equal(stopped.expiry.stop, true);
  assert.equal(stopped.expiry.reply, EXPIRED_REPLY);

  // The boundary the other way: a prompt of age exactly the TTL still queues.
  const alive = enqueue(queue, { now: T0 + PROMPT_TTL_MS, prompt: promptMessage() });
  assert.equal(alive.outcome, OUTCOME.QUEUED);
});

// ---------------------------------------------------------------------------
// Expiry on the consumer's clock, not the sweeper's (D168 as amended)
// ---------------------------------------------------------------------------

test('expire is a pure function of each prompt\'s at, the TTL and now', () => {
  const queue = enqueueOne(makeQueue()).queue;
  const expired = expire(queue, { now: T0 + PROMPT_TTL_MS + 1 });
  assert.equal(expired.ok, true);
  assert.equal(expired.expired.length, 1);
  assert.equal(expired.queue.prompts.length, 0);
  // Still nothing at the boundary.
  assert.equal(expire(queue, { now: T0 + PROMPT_TTL_MS }).expired.length, 0);
});

test('when the sweeper runs decides nothing: the consumer\'s own clock does', () => {
  // The owner-run sweeper (D168) deletes the stored prompt; the consumer may
  // be down while it runs and come back with nothing but its own copy. So the
  // expiry is asserted to depend on `now` alone and not on any sweep having
  // happened: a queue advanced by two earlier expiries at the same instant as
  // one that was never swept must expire identically.
  const queue = enqueueOne(makeQueue({ promptTtlMs: 2 * HOUR })).queue;
  const sweptTwice = expire(expire(queue, { now: T0 + HOUR }).queue, { now: T0 + 2 * HOUR });
  const never = expire(queue, { now: T0 + 2 * HOUR });
  assert.deepEqual(sweptTwice.expired, never.expired);
  assert.deepEqual(sweptTwice.queue, never.queue);
  // And the expiry carries the answer the prompt's death is owed.
  const late = expire(
    enqueueOne(
      makeQueue({ promptTtlMs: HOUR }),
      { options: ['ship', 'hold'] },
      { safe: 'hold' },
    ).queue,
    { now: T0 + HOUR + 1 },
  );
  assert.equal(late.expired[0].reply, 'hold');
  assert.equal(late.expired[0].stop, false);
});

test('expire refuses a clock that went backwards and an unreadable queue', () => {
  const queue = enqueueOne(makeQueue()).queue;
  assert.equal(expire(queue, { now: T0 - 1 }).code, REFUSAL.NOW_WENT_BACKWARDS);
  assert.equal(expire(queue, {}).code, REFUSAL.MISSING_FIELD);
  assert.equal(expire(queue, { now: T0, x: 1 }).code, REFUSAL.UNKNOWN_FIELD);
});

test('a clock that leaps forward is refused, and the watermark does not move', () => {
  // `now` is monotone: it is written into the queue as the watermark, and every
  // later reading earlier than it is refused `now-went-backwards`. Without a
  // forward bound, one bad reading (or one hostile caller) writing TIME_MAX
  // would refuse every honest `now` after it for ever -- the queue bricked by a
  // single event. P1.1 bounds the same leap on the mailbox (`now-jumped`,
  // `MAX_FORWARD_JUMP_MS`), and this module imports that bound rather than
  // restating it, so the two cannot drift.
  const queue = enqueueOne(makeQueue()).queue;
  assert.equal(MAX_JUMP_MS, MAX_FORWARD_JUMP_MS, "P1.1's bound, imported");
  assert.equal(REFUSAL.NOW_JUMPED, 'now-jumped', "P1.1's code, spelled the same");

  const jumped = expire(queue, { now: T0 + MAX_JUMP_MS + 1 });
  assert.equal(jumped.ok, false);
  assert.equal(jumped.code, REFUSAL.NOW_JUMPED);
  assert.equal(jumped.field, 'now');
  assert.ok(Object.isFrozen(jumped));
  // The far-future `now` is refused WITHOUT being written: the queue the caller
  // holds is untouched, which is what makes recovery the next line.
  assert.equal(queue.now, T0);
  // Recovery: an honest `now` after a refused leap is accepted, because the
  // refused value never became the watermark.
  const honest = expire(queue, { now: T0 + MINUTE });
  assert.equal(honest.ok, true, 'a refused jump must not brick the queue');
  assert.equal(honest.queue.now, T0 + MINUTE);
  // The boundary itself is a jump this module accepts -- exactly the leap, not
  // one millisecond more -- and the same bound holds on every operation that
  // opens a queue.
  assert.equal(expire(queue, { now: T0 + MAX_JUMP_MS }).ok, true);
  assert.equal(expire(queue, { now: T0 + MAX_JUMP_MS + 1 }).code, REFUSAL.NOW_JUMPED);
  assert.equal(pending(queue, { now: T0 + MAX_JUMP_MS + 1 }).code, REFUSAL.NOW_JUMPED);
  assert.equal(announce(queue, { now: T0 + MAX_JUMP_MS + 1 }).code, REFUSAL.NOW_JUMPED);
  assert.equal(
    enqueue(queue, { now: T0 + MAX_JUMP_MS + 1, prompt: promptMessage({ id: 'p9' }) }).code,
    REFUSAL.NOW_JUMPED,
  );
  assert.equal(
    answer(queue, { now: T0 + MAX_JUMP_MS + 1, answer: answerMessage() }).code,
    REFUSAL.NOW_JUMPED,
  );
  // A `now` at the ceiling is a leap like any other: the bound is the leap, not
  // the value, so no single reading can put the watermark at TIME_MAX.
  assert.equal(expire(queue, { now: TIME_MAX }).code, REFUSAL.NOW_JUMPED);
  // And a clock that went backwards is still refused first, by its own code:
  // the two faults are not one.
  assert.equal(expire(queue, { now: T0 - 1 }).code, REFUSAL.NOW_WENT_BACKWARDS);
});

test('every MUTATING operation writes the reading it accepted into the queue it hands back', () => {
  // The monotone rule is not only a check on the way in: an accepted `now`
  // becomes the queue's watermark, which is what makes the NEXT call's
  // `now-went-backwards` possible. One path that forgot to write it would let a
  // clock step back after an enqueue, an answer or an announce -- re-arming
  // reminders and reviving a prompt's due state -- while looking exactly like a
  // queue that had never heard of the later reading.
  //
  // MUTATING is the word that matters, and the exceptions below are pinned
  // rather than assumed: a read (`pending`) and an enqueue that changed nothing
  // (`duplicate`, `expired`) hand back the queue they were given, so the
  // reading they accepted is NOT recorded -- deliberately, because there is no
  // state for it to be recorded in. A later call with an earlier `now` is
  // therefore accepted after them, which is the same fact from the other side.
  const queue = enqueueOne(makeQueue()).queue;
  assert.equal(queue.now, T0);
  const later = T0 + HOUR;
  const paths = [
    ['enqueue', (q, now) => enqueue(q, { now, prompt: promptMessage({ id: 'p2', at: now }) })],
    ['expire', (q, now) => expire(q, { now })],
    ['answer', (q, now) => answer(q, { now, answer: answerMessage({ at: now }) })],
    ['announce', (q, now) => announce(q, { now })],
  ];
  for (const [name, act] of paths) {
    const moved = act(queue, later);
    assert.equal(moved.ok, true, `${name}: ${JSON.stringify(moved)}`);
    assert.equal(moved.queue.now, later, `${name} writes the reading it accepted`);
    // And the rule that write is for, on the queue it just handed back.
    assert.equal(act(moved.queue, later - 1).code, REFUSAL.NOW_WENT_BACKWARDS, name);
  }

  // The exceptions, one by one: each hands back the very queue it was given.
  const read = pending(queue, { now: later });
  assert.equal(read.ok, true);
  assert.deepEqual(read.queue, queue, 'a read hands back the queue it was given');
  assert.equal(read.queue.now, T0, 'a read records no reading');
  const duplicate = enqueue(queue, { now: later, prompt: promptMessage() });
  assert.equal(duplicate.outcome, OUTCOME.DUPLICATE);
  assert.equal(duplicate.queue.now, T0, 'a duplicate changed nothing, so it records nothing');
  const dead = enqueue(makeQueue(), { now: T0 + PROMPT_TTL_MS + 1, prompt: promptMessage({ at: T0 }) });
  assert.equal(dead.outcome, OUTCOME.EXPIRED, JSON.stringify(dead));
  assert.equal(dead.queue.now, T0, 'an already-dead delivery is not a queue that moved on');
});

test('the count across installations refuses a clock behind an installation it read', () => {
  // `announcement`'s own check is pinned beside it; this is the same rule in
  // the other cross-account read, reached one loop in. The count is of a world
  // that has already moved on otherwise -- and the check is per installation,
  // not once for the list, so one queue ahead is enough.
  const queues = twoAccounts();
  const ahead = queues.map((q, index) => (index === 1 ? { ...q, now: T0 + MINUTE } : q));
  const refused = pendingAcross(ahead, { now: T0 });
  assert.equal(refused.ok, false);
  assert.equal(refused.code, REFUSAL.NOW_WENT_BACKWARDS);
  assert.equal(refused.field, 'now');
  assert.ok(Object.isFrozen(refused));
  // At the reading the installation was last seen at, the same list is read.
  assert.equal(pendingAcross(ahead, { now: T0 + MINUTE }).ok, true);
  // One installation, behind: the same rule, the same code.
  const single = [{ ...queues[0], now: T0 + MINUTE }];
  assert.equal(pendingAcross(single, { now: T0 }).code, REFUSAL.NOW_WENT_BACKWARDS);
  // And the forward jump is deliberately NOT bounded here: nothing is written,
  // so a far-future reading is a listing, not a fault.
  assert.equal(pendingAcross(queues, { now: T0 + MAX_JUMP_MS + 1 }).ok, true);
});

test('the reads answer over a queue the caller has already expired, and say what they see', () => {
  // A held prompt dies while it waits, and the reads do NOT apply the TTL to
  // it. That is deliberate, and it is the caller's ordering that makes it safe:
  // `expire` is what kills a prompt, it is P6.7 that calls it -- on the
  // consumer's own clock, before it reads -- and expiry YIELDS the safe answer
  // or a stop that the orchestrator is owed exactly once (D168 as amended). A
  // listing that quietly dropped a dead prompt would destroy that answer, which
  // is the one thing this module refuses to do anywhere else (a full queue is
  // refused, never evicted).
  //
  // So the invariant is a PRECONDITION, stated here and in the README: the
  // reads are over a swept queue. This test pins both halves of it -- what a
  // read reports when the caller has not swept, and that the sweep clears it.
  const queue = enqueueOne(makeQueue()).queue;
  const dead = T0 + PROMPT_TTL_MS + 1; // one millisecond past the TTL
  const swept = expire(queue, { now: dead });
  assert.equal(swept.expired.length, 1, "expire's own verdict at that now");
  assert.equal(swept.expired[0].reply, EXPIRED_REPLY, 'the answer the orchestrator is owed');
  assert.equal(swept.expired[0].stop, true, 'no safe answer, so it is told to stop');
  assert.equal(swept.queue.prompts.length, 0);

  // Unswept, at that same now: every read still sees a live, due question.
  const listed = pending(queue, { now: dead });
  assert.equal(listed.pending.length, 1);
  assert.equal(listed.pending[0].due, true, 'a dead prompt still reads as due');
  assert.equal(announce(queue, { now: dead }).spoken.length, 1, 'and is still read out');
  assert.equal(announcement([queue], { now: dead }).count, 1);
  assert.equal(announcement([queue], { now: dead }).text, 'One question waiting');
  // `pendingAcross` is a no-TTL read too, and it is the one a cross-account view
  // draws every installation's row from (D170): a per-installation listing that
  // dropped a dead prompt would hide the question whose safe answer or stop the
  // orchestrator is owed -- silently, per installation, which is exactly the
  // failure this module refuses everywhere else.
  const across = pendingAcross([queue], { now: dead });
  assert.equal(across.count, 1);
  assert.equal(across.accounts[0].pending.length, 1, 'a per-installation listing keeps it');
  assert.equal(across.accounts[0].pending[0].due, true);
  assert.equal(
    evaluateInterrupt({ queue, now: dead, switch: SWITCH.WIDEN }).reason,
    INTERRUPT.PROMPT,
    'and can raise the widen interrupt until it is swept',
  );

  // Swept: none of them reports it, at the same `now`.
  assert.equal(pending(swept.queue, { now: dead }).pending.length, 0);
  assert.equal(announce(swept.queue, { now: dead }).spoken.length, 0);
  assert.equal(announcement([swept.queue], { now: dead }).count, 0);
  assert.equal(announcement([swept.queue], { now: dead }).text, null);
  const acrossSwept = pendingAcross([swept.queue], { now: dead });
  assert.equal(acrossSwept.count, 0);
  assert.equal(acrossSwept.accounts[0].pending.length, 0);
  assert.equal(
    evaluateInterrupt({ queue: swept.queue, now: dead, switch: SWITCH.WIDEN }).reason,
    null,
  );

  // And the door refuses to hold one in the first place, which is the same rule
  // one call earlier: a delivery that arrives dead is answered, not queued.
  const late = enqueue(makeQueue(), { now: dead, prompt: promptMessage({ at: T0 }) });
  assert.equal(late.outcome, OUTCOME.EXPIRED);
  assert.equal(late.queue.prompts.length, 0);
});

test('a stored prompt is re-checked against the `at` bound on every read', () => {
  // The schema accepts `at: 0` (and `at: -0`, which is numerically zero) and
  // any safe integer up to `Number.MAX_SAFE_INTEGER`; this module will not
  // carry either once it has been read. A queue is a value the caller hands
  // back, so an entry holding one of them is refused rather than trusted --
  // otherwise a hand-built queue could carry a prompt whose age nothing bounds.
  const queue = enqueueOne(makeQueue()).queue;
  const held = queue.prompts[0];
  for (const at of [0, -0, Number.MAX_SAFE_INTEGER]) {
    const broken = { ...queue, prompts: [{ ...held, prompt: { ...held.prompt, at } }] };
    const result = expire(broken, { now: T0 });
    assert.equal(result.ok, false, `at: ${String(at)} must not be read`);
    assert.equal(result.code, REFUSAL.INVALID_ENTRY, `at: ${String(at)}`);
  }
  // The same bound the writer is held to at the door: one is refused by
  // `zero-timestamp`/`negative-zero`, which is why the reader refuses the entry
  // rather than reading a time the writer could never have sent.
  assert.equal(enqueue(queue, { now: T0, prompt: promptMessage({ at: 0 }) }).code, REFUSAL.ZERO_TIMESTAMP);
  assert.equal(enqueue(queue, { now: T0, prompt: promptMessage({ at: -0 }) }).code, REFUSAL.NEGATIVE_ZERO);
  assert.equal(
    enqueue(queue, { now: T0, prompt: promptMessage({ at: Number.MAX_SAFE_INTEGER }) }).code,
    REFUSAL.FUTURE_TIMESTAMP,
  );
  // A queue whose entries are all inside the bounds is still read as before.
  assert.equal(expire(queue, { now: T0 + MINUTE }).ok, true);
});

test('enqueue will not write a prompt the read path would refuse', () => {
  // The read path refuses a prompt past the module's own ceiling, so the WRITE
  // path must not be able to produce one: `open` accepts `now` up to `TIME_MAX`
  // and the future tolerance reaches past it, so a prompt at `TIME_MAX + 1`
  // used to be written into a queue that every later read then refused -- the
  // bricking the `now-jumped` refusal exists to prevent, reached one call over.
  const queue = makeQueue({ now: TIME_MAX });
  const past = enqueue(queue, { now: TIME_MAX, prompt: promptMessage({ at: TIME_MAX + 1 }) });
  assert.equal(past.ok, false);
  assert.equal(past.code, REFUSAL.FUTURE_TIMESTAMP);
  assert.equal(past.field, 'prompt.at');

  // The last prompt the writer will take is the last one the reader accepts,
  // and the queue it leaves behind is readable.
  const ceiling = enqueue(queue, { now: TIME_MAX, prompt: promptMessage({ at: TIME_MAX }) });
  assert.equal(ceiling.ok, true, JSON.stringify(ceiling));
  assert.equal(ceiling.queue.prompts.length, 1);
  assert.equal(expire(ceiling.queue, { now: TIME_MAX }).ok, true);
  assert.equal(pending(ceiling.queue, { now: TIME_MAX }).ok, true);
  assert.equal(answer(ceiling.queue, { now: TIME_MAX, answer: answerMessage({ at: TIME_MAX }) }).ok, true);
});

// ---------------------------------------------------------------------------
// Answers: the TTL, except a destructive prompt's confirmation (D173)
// ---------------------------------------------------------------------------

test('an answer is accepted within the prompt\'s TTL, not P1.1\'s command window', () => {
  // The headline of D173: a consumer offline longer than the six-hour command
  // window but inside the 72-hour TTL still accepts a queued prompt's answer.
  const queue = enqueueOne(makeQueue()).queue;
  // The user answered as soon as they read the question (at T0) and the
  // consumer only came back twenty hours later -- three times past the
  // six-hour command window, well inside the 72-hour TTL.
  const late = T0 + 20 * HOUR;
  assert.ok(late - T0 > 6 * HOUR, 'the fixture must be past the command window');
  const settled = answer(queue, { now: late, answer: answerMessage({ at: T0, text: 'ship it' }) });
  assert.equal(settled.ok, true, JSON.stringify(settled));
  assert.equal(settled.outcome, OUTCOME.ANSWERED);
  assert.equal(settled.kind, MAILBOX_KIND.PROMPT);
  assert.equal(settled.windowMs, PROMPT_TTL_MS);
  assert.equal(settled.queue.prompts.length, 0, 'an answered prompt is not pending');

  // P1.1's own machine classifies the very same delivery as a command and
  // refuses it: that is the window an answer to a destructive prompt takes,
  // and the difference between the two lines is the whole of D173. Asserted by
  // running P1.1 rather than by restating its arithmetic here.
  const mailbox = initialState({ now: T0, promptTtlMs: PROMPT_TTL_MS });
  assert.equal(mailbox.ok, true);
  const asCommand = step(mailbox.state, {
    type: 'deliver',
    now: late,
    kind: MAILBOX_KIND.COMMAND,
    message: answerMessage({ at: T0 }),
  });
  assert.equal(asCommand.ok, false);
  assert.equal(asCommand.code, 'replay', 'a command of that age is a replay in P1.1');
  const asPrompt = step(mailbox.state, {
    type: 'deliver',
    now: late,
    kind: MAILBOX_KIND.PROMPT,
    message: answerMessage({ at: T0 }),
  });
  assert.equal(asPrompt.ok, true, 'and the same delivery as a prompt is accepted');
});

test('an answer to a destructive prompt is a confirmation and takes the command window', () => {
  const queue = enqueueOne(makeQueue(), {}, { destructive: true }).queue;
  const written = T0 + HOUR;
  const confirmed = actionMessage({ at: written, action: actionRecord(written) });

  // Confirmed five hours after the user tapped it: inside the command window,
  // and judged as a command -- the kind P1.1 applies the six-hour window to.
  const inside = answer(queue, { now: written + 5 * HOUR, answer: confirmed });
  assert.equal(inside.ok, true, JSON.stringify(inside));
  assert.equal(inside.kind, MAILBOX_KIND.COMMAND);
  assert.equal(inside.windowMs, 6 * HOUR);
  assert.equal(inside.destructive, true);

  // Exactly at the window: still a confirmation, which is the same inclusive
  // boundary P1.1's `now - at > window` draws.
  assert.equal(answer(queue, { now: written + 6 * HOUR, answer: confirmed }).ok, true);

  // One millisecond past it, while the PROMPT is still alive by days: refused.
  // The CONFIRMATION's own time is what decides once the answer is a
  // confirmation -- §3.5 checks the command window from the confirmation time,
  // not from the message's `at`, which is only the envelope's clock.
  const refused = answer(queue, { now: written + 6 * HOUR + 1, answer: confirmed });
  assert.equal(refused.ok, false);
  assert.equal(refused.code, REFUSAL.REPLAY);
  assert.equal(refused.field, 'answer.action.confirmation.at');

  // Both halves of that, told apart: the same record with a fresh `at` and the
  // old confirmation is still a replay, and one whose `at` is old but whose
  // confirmation is fresh is accepted.
  const restamped = actionMessage({ at: written + 6 * HOUR + 1, action: actionRecord(written) });
  assert.equal(answer(queue, { now: written + 6 * HOUR + 1, answer: restamped }).code, REFUSAL.REPLAY);
  const fresh = actionMessage({ at: written, action: actionRecord(written + 6 * HOUR) });
  assert.equal(
    answer(queue, { now: written + 6 * HOUR + 1, answer: fresh }).ok,
    true,
    'a fresh confirmation is still accepted',
  );
});

test('a plain answer to a destructive prompt is refused: only a confirmation answers it', () => {
  // §3.5, from D169's widening after review round 2: an answer to a destructive
  // prompt is accepted ONLY as a MAC-verified action record carrying a
  // confirmation -- never as a plain `answer`, which the consumer must refuse.
  // The MAC is P6.7's to verify; the SHAPE is this module's, and accepting a
  // plain `answer` here would leave P6.7 a command it has to refuse.
  const queue = enqueueOne(makeQueue(), {}, { destructive: true }).queue;
  const plain = answer(queue, { now: T0, answer: answerMessage() });
  assert.equal(plain.ok, false);
  assert.equal(plain.code, REFUSAL.NOT_A_CONFIRMATION);
  assert.equal(plain.field, 'answer.kind');
  assert.ok(Object.isFrozen(plain));
  assert.equal(queue.prompts.length, 1, 'nothing left the queue on the refusal');

  // And the converse: an action record answering a prompt whose effect is not
  // destructive is not the shape that prompt waits for either.
  const quiet = enqueueOne(makeQueue()).queue;
  const wrong = answer(quiet, { now: T0, answer: actionMessage() });
  assert.equal(wrong.ok, false);
  assert.equal(wrong.code, REFUSAL.NOT_AN_ANSWER);
  assert.equal(wrong.field, 'answer.kind');

  // A `message` is neither shape, and an action record with no `reply_to`
  // names no prompt this queue holds.
  const message = { id: 'm1', sender: 'phone', text: 'ok', at: T0, mac: MAC, kind: 'message' };
  assert.equal(answer(quiet, { now: T0, answer: message }).code, REFUSAL.NOT_AN_ANSWER);
  const unwired = actionMessage();
  delete unwired.reply_to;
  assert.equal(answer(queue, { now: T0, answer: unwired }).code, REFUSAL.UNKNOWN_PROMPT);
});

test('an action record is checked as an action record, not merely accepted by kind', () => {
  const queue = enqueueOne(makeQueue(), {}, { destructive: true }).queue;

  // The schema's own rules apply to the record. The fault it names is a
  // SUB-path (`action.verb`, `action.extra`), and a sub-path is not one of the
  // fields this module's message vocabulary names -- so the refusal reports the
  // answer as a whole, exactly as it does for an unknown key at the top level.
  // The CODE is still the schema's: the caller branches on the reason.
  const unknownVerb = actionMessage({ action: { ...actionRecord(T0), verb: 'mergee' } });
  const refused = answer(queue, { now: T0, answer: unknownVerb });
  assert.equal(refused.ok, false);
  assert.equal(refused.code, 'unknown-verb');
  assert.equal(refused.field, 'answer');
  const unknownKey = actionMessage({ action: { ...actionRecord(T0), extra: 1 } });
  assert.equal(answer(queue, { now: T0, answer: unknownKey }).code, 'unknown-field');

  // A confirmation time is a timestamp like any other, and this module's own
  // bounds apply to it: `at: 0` would be a confirmation measured from an unset
  // counter, and a future one is a clock this module will not read.
  assert.equal(
    answer(queue, { now: T0, answer: actionMessage({ action: actionRecord(0) }) }).code,
    REFUSAL.ZERO_TIMESTAMP,
  );
  assert.equal(
    answer(queue, { now: T0, answer: actionMessage({ action: actionRecord(-0) }) }).code,
    REFUSAL.NEGATIVE_ZERO,
  );
  assert.equal(
    answer(queue, { now: T0, answer: actionMessage({ action: actionRecord(T0 + 30 * DAY) }) }).code,
    REFUSAL.FUTURE_TIMESTAMP,
  );
});

test("the window an answer is judged by is P1.1's own kind mapping", () => {
  assert.equal(acceptanceKind(false), MAILBOX_KIND.PROMPT);
  assert.equal(acceptanceKind(true), MAILBOX_KIND.COMMAND);
  // The kinds are P1.1's vocabulary and not a second one written here.
  assert.equal(MAILBOX_KIND.PROMPT, 'prompt');
  assert.equal(MAILBOX_KIND.COMMAND, 'command');
});

test("an answer older than its window is refused with P1.1's code for that window", () => {
  // The window is P1.1's own mapping (`acceptanceKind`), so the refusal has to
  // be too. P1.1 answers `expired` to a delivery past the prompt window and
  // `replay` to a command past the six-hour one (mailbox/index.js's deliver),
  // and P6.7 passes `acceptanceKind`'s kind to exactly that call: a code this
  // module picked differently would have the app tell the user one thing and
  // the mailbox do another.
  const queue = enqueueOne(makeQueue()).queue;
  const stale = T0 - 80 * HOUR; // older than the whole 72-hour TTL
  assert.ok(T0 + HOUR - stale > PROMPT_TTL_MS, 'the fixture must be past the TTL');

  const refused = answer(queue, { now: T0 + HOUR, answer: answerMessage({ at: stale }) });
  assert.equal(refused.ok, false, JSON.stringify(refused));
  assert.equal(refused.code, REFUSAL.EXPIRED);
  assert.equal(refused.field, 'answer.at');
  // Nothing is owed: the PROMPT is alive, so this refusal carries no expiry --
  // unlike the dead-prompt one, whose `expiry` is the safe answer the
  // orchestrator must still be handed. Same code, two facts, told apart by
  // `field` and by that field's presence.
  assert.equal(refused.expiry, undefined);

  const mailbox = initialState({ now: T0, promptTtlMs: PROMPT_TTL_MS });
  assert.equal(mailbox.ok, true);
  const asPrompt = step(mailbox.state, {
    type: 'deliver',
    now: T0 + HOUR,
    kind: MAILBOX_KIND.PROMPT,
    message: answerMessage({ at: stale }),
  });
  assert.equal(asPrompt.ok, false);
  assert.equal(asPrompt.code, 'expired', 'P1.1 calls a prompt-window delivery of that age expired');

  // And the confirmation's own age is still a `replay`, which the destructive
  // test above pins: the mapping decides the code, not the operation.
  const confirmation = enqueueOne(makeQueue(), {}, { destructive: true }).queue;
  const old = answer(confirmation, {
    now: T0 + 7 * HOUR,
    answer: actionMessage({ at: T0, action: actionRecord(T0) }),
  });
  assert.equal(old.ok, false);
  assert.equal(old.code, REFUSAL.REPLAY);
});

test('a redelivery cannot clear an announcement, and an answered id is the ledger\'s', () => {
  // What `announcedAt` buys, told straight: a duplicate is answered from the
  // queue it is already in, so the announcement stands and the agent cannot
  // make the phone say a held question a second time by sending it again.
  const held = announce(enqueueOne(makeQueue()).queue, { now: T0 + HOUR }).queue;
  assert.equal(held.prompts[0].announcedAt, T0 + HOUR);
  const twice = enqueue(held, { now: T0 + HOUR, prompt: promptMessage() });
  assert.equal(twice.outcome, OUTCOME.DUPLICATE);
  assert.equal(twice.queue.prompts[0].announcedAt, T0 + HOUR, 'the announcement stands');

  // And the limit that is NOT this module's: answering settles the prompt and
  // drops the entry, so the same message delivered again is a fresh question --
  // queued, unannounced, and read out at the next pause. Remembering that an id
  // was ANSWERED is P6.7's outbox ledger, the same division of labour P1.1's
  // dedupe ledger draws for delivery; this module holds what is outstanding and
  // nothing else.
  const settled = answer(held, { now: T0 + HOUR, answer: answerMessage({ at: T0 + HOUR }) }).queue;
  assert.equal(settled.prompts.length, 0, 'an answered prompt is gone from the queue');
  const redelivered = enqueue(settled, { now: T0 + HOUR, prompt: promptMessage() });
  assert.equal(redelivered.outcome, OUTCOME.QUEUED);
  assert.equal(redelivered.queue.prompts[0].announcedAt, null);
  const spoke = announce(redelivered.queue, { now: T0 + HOUR });
  assert.equal(spoke.spoken.length, 1, 'it is spoken again: the queue does not remember answers');
});

test('an answer to a dead prompt is refused, with the expiry it is owed', () => {
  const queue = enqueueOne(makeQueue(), {}, {}).queue;
  const before = answer(queue, { now: T0 + PROMPT_TTL_MS, answer: answerMessage({ at: T0 + PROMPT_TTL_MS }) });
  assert.equal(before.ok, true, 'the prompt is alive at the boundary');
  const after = answer(queue, { now: T0 + PROMPT_TTL_MS + 1, answer: answerMessage({ at: T0 + PROMPT_TTL_MS + 1 }) });
  assert.equal(after.ok, false);
  assert.equal(after.code, REFUSAL.EXPIRED);
  assert.equal(after.expiry.reply, EXPIRED_REPLY);
  assert.equal(after.expiry.stop, true);
  // The user's late answer is not what is handed over: the prompt expired, and
  // an answer to an expired question is an answer to a changed world.
  assert.equal(after.expiry.safe, null);
});

test('answer refuses an unreadable answer and an unknown prompt', () => {
  const queue = enqueueOne(makeQueue()).queue;
  // A prompt is a well-formed message, and it is not an answer: the two kinds
  // are not interchangeable, and this module reads the kind off the validated
  // message rather than assuming the caller sent what it meant to.
  assert.equal(answer(queue, { now: T0, answer: promptMessage() }).code, REFUSAL.NOT_AN_ANSWER);
  assert.equal(answer(queue, { now: T0, answer: answerMessage({ reply_to: 'nope' }) }).code, REFUSAL.UNKNOWN_PROMPT);
  assert.equal(answer(queue, { now: T0, answer: answerMessage({ text: '' }) }).code, 'empty-string');
  assert.equal(answer(queue, { now: T0, answer: answerMessage({ at: 0 }) }).code, REFUSAL.ZERO_TIMESTAMP);
  assert.equal(answer(queue, { now: T0, answer: answerMessage({ at: -0 }) }).code, REFUSAL.NEGATIVE_ZERO);
  assert.equal(
    answer(queue, { now: T0, answer: answerMessage({ at: T0 + 30 * DAY }) }).code,
    REFUSAL.FUTURE_TIMESTAMP,
  );
  assert.equal(answer(queue, { now: T0 - 1, answer: answerMessage() }).code, REFUSAL.NOW_WENT_BACKWARDS);
  assert.equal(answer(queue, { now: T0, answer: answerMessage(), reply: 'x' }).code, REFUSAL.UNKNOWN_FIELD);
  // The same line as `enqueue` draws for a prompt: an unknown key is the
  // writer's name and is not echoed; a field the schema defines is named.
  const unknown = answer(queue, { now: T0, answer: answerMessage({ nonsense: true }) });
  assert.equal(unknown.code, 'unknown-field');
  assert.equal(unknown.field, 'answer');
  const unreadable = answerMessage();
  delete unreadable.reply_to;
  const schemaField = answer(queue, { now: T0, answer: unreadable });
  assert.equal(schemaField.code, 'missing-field');
  assert.equal(schemaField.field, 'answer.reply_to');
});

test('an answer removes exactly the prompt it answers', () => {
  let queue = enqueueOne(makeQueue(), { id: 'p1' }).queue;
  queue = enqueue(queue, { now: T0, prompt: promptMessage({ id: 'p2', at: T0 + 1_000 }) }).queue;
  const settled = answer(queue, { now: T0, answer: answerMessage({ reply_to: 'p2', at: T0 + 1_000 }) });
  assert.equal(settled.ok, true);
  assert.deepEqual(settled.queue.prompts.map((entry) => entry.prompt.id), ['p1']);
});

// ---------------------------------------------------------------------------
// Reminder escalation (§3.4)
// ---------------------------------------------------------------------------

test('reminderStage is a pure function of the clock: an hour, then a day', () => {
  assert.equal(REMINDER_AFTER_MS, HOUR);
  assert.equal(REMINDER_INTERVAL_MS, DAY);
  assert.equal(reminderStage(T0, T0), 0);
  assert.equal(reminderStage(T0, T0 + HOUR - 1), 0);
  assert.equal(reminderStage(T0, T0 + HOUR), 1, 'an hour is the first reminder, inclusive');
  assert.equal(reminderStage(T0, T0 + HOUR + DAY - 1), 1);
  assert.equal(reminderStage(T0, T0 + HOUR + DAY), 2);
  assert.equal(reminderStage(T0, T0 + HOUR + 3 * DAY), 4);
  // A stage is a number the text screen renders and this module compares, so a
  // value that is not a time is refused rather than answered with `NaN` -- which
  // is neither 0 nor any stage, and compares as nothing at all. The two
  // arguments are checked as `isExpired` checks its three.
  for (const [at, now] of [
    [undefined, T0],
    [null, T0],
    ['x', T0],
    [NaN, T0],
    [0, T0],
    [T0, undefined],
    [T0, 'x'],
    [T0, null],
  ]) {
    const result = reminderStage(at, now);
    assert.equal(result.ok, false, `reminderStage(${String(at)}, ${String(now)}) must refuse`);
    assert.equal(result.code, at === T0 ? REFUSAL.INVALID_NOW : REFUSAL.INVALID_AT);
    assert.equal(result.field, at === T0 ? 'now' : 'at');
    assert.ok(Object.isFrozen(result));
  }
});

test('a pause speaks what is due, then the count alone, then escalates', () => {
  let queue = enqueueOne(makeQueue()).queue;
  queue = enqueue(queue, { now: T0, prompt: promptMessage({ id: 'p2', at: T0 + 1_000 }) }).queue;

  // The first natural pause: both are due, and both are marked spoken.
  const first = announce(queue, { now: T0 + 2 * MINUTE });
  assert.equal(first.ok, true);
  assert.equal(first.spoken.length, 2);
  assert.deepEqual(first.spoken.map((entry) => entry.prompt.id), ['p1', 'p2']);
  assert.equal(first.spoken[0].stage, 0);
  assert.equal(first.queue.prompts[0].announcedAt, T0 + 2 * MINUTE);
  // What is spoken is the entry as the queue now holds it: a caller that
  // persists the spoken list cannot persist an announcement time the queue has
  // already moved past.
  assert.equal(first.spoken[0].announcedAt, T0 + 2 * MINUTE);
  assert.equal(Object.hasOwn(first.spoken[0], 'due'), false);

  // The next pause a minute later: nothing is due -- the user has just heard
  // these -- and the announcement is empty rather than repeating.
  const second = announce(first.queue, { now: T0 + 3 * MINUTE });
  assert.equal(second.spoken.length, 0);
  assert.equal(second.queue.prompts[0].announcedAt, T0 + 2 * MINUTE, 'nothing is re-marked');

  // An hour after the first prompt was asked, it escalates and is due again.
  // Both are asked a second apart, so the pause is a second past the hour to
  // cover both -- the schedule is anchored to each prompt's own `at`.
  const third = announce(second.queue, { now: T0 + HOUR + 2_000 });
  assert.deepEqual(third.spoken.map((entry) => entry.prompt.id), ['p1', 'p2']);
  assert.equal(third.spoken[0].stage, 1);
  assert.equal(third.queue.prompts[0].announcedAt, T0 + HOUR + 2_000);

  // Announced late, a stage is not announced twice: the announcement at
  // +2 days covers the day-one reminder and leaves the day-two one due.
  const fourth = announce(third.queue, { now: T0 + 2 * DAY });
  assert.equal(fourth.spoken.length, 2);
  const fifth = announce(fourth.queue, { now: T0 + 2 * DAY + MINUTE });
  assert.equal(fifth.spoken.length, 0);
  const sixth = announce(fifth.queue, { now: T0 + 2 * HOUR + 2 * DAY });
  assert.equal(sixth.spoken.length, 2);
  assert.equal(sixth.spoken[0].stage, 3, 'the day-two reminder');
});

test('a prompt announced at its current stage is not due again until its hour', () => {
  // The reachable case, and the reason the stage-0 branch exists: a prompt may
  // be written up to the future tolerance AHEAD of the clock. An `at` inside
  // the last hour of that tolerance has its stage-0 reminder time (`at - 23h`,
  // which the day-long interval makes land in the future) still ahead of the
  // announcement, so without the branch the question would be reported due at
  // every pause and read out for ever.
  const built = enqueue(makeQueue({ futureToleranceMs: MAX_FUTURE_TOLERANCE_MS }), {
    now: T0,
    prompt: promptMessage({ at: T0 + MAX_FUTURE_TOLERANCE_MS }),
  });
  assert.equal(built.ok, true, JSON.stringify(built));

  const first = announce(built.queue, { now: T0 + MINUTE });
  assert.equal(first.spoken.length, 1, 'the first pause reads the question');
  assert.equal(first.spoken[0].stage, 0);
  assert.equal(first.spoken[0].announcedAt, T0 + MINUTE);

  const second = announce(first.queue, { now: T0 + 2 * MINUTE });
  assert.equal(second.spoken.length, 0, 'the user has just heard it: never twice');
  const listed = pending(second.queue, { now: T0 + 2 * MINUTE });
  assert.equal(listed.pending[0].due, false);
  assert.equal(listed.pending[0].stage, 0);
  // And widening does not turn a question just read into an interruption.
  assert.equal(
    evaluateInterrupt({ queue: second.queue, now: T0 + 2 * MINUTE, switch: SWITCH.WIDEN }).reason,
    null,
  );

  // The hour after the prompt's own `at`, it comes round like any other.
  const third = announce(second.queue, { now: T0 + MAX_FUTURE_TOLERANCE_MS + HOUR + 1 });
  assert.equal(third.spoken.length, 1);
  assert.equal(third.spoken[0].stage, 1);
});

test('pending lists per installation, escalated first', () => {
  // p1 was asked at the start and announced at the first pause, so by the
  // listing its hour has passed and it has escalated; p2 and p3 were asked a
  // minute later and were read out at the second pause.
  let queue = enqueueOne(makeQueue(), { id: 'p1' }).queue;
  queue = announce(queue, { now: T0 + MINUTE }).queue;
  queue = enqueue(queue, { now: T0 + MINUTE, prompt: promptMessage({ id: 'p2', at: T0 + MINUTE }) }).queue;
  queue = enqueue(queue, { now: T0 + MINUTE, prompt: promptMessage({ id: 'p3', at: T0 + MINUTE }) }).queue;
  queue = announce(queue, { now: T0 + 2 * MINUTE }).queue;

  const listed = pending(queue, { now: T0 + HOUR });
  assert.equal(listed.ok, true, JSON.stringify(listed));
  assert.deepEqual(listed.pending.map((entry) => entry.prompt.id), ['p1', 'p2', 'p3']);
  // The escalated one is listed first, and is due to be read again.
  assert.equal(listed.pending[0].stage, 1);
  assert.equal(listed.pending[0].due, true);
  assert.equal(listed.pending[0].announcedAt, T0 + MINUTE);
  assert.equal(listed.pending[1].announcedAt, T0 + 2 * MINUTE);
  // The two just read are still listed -- they are pending -- and are not due.
  assert.equal(listed.pending[1].stage, 0);
  assert.equal(listed.pending[1].due, false);
  assert.deepEqual(listed.queue, queue, 'listing changes nothing');

  // Within a stage, oldest first.
  const two = enqueue(
    enqueue(makeQueue(), { now: T0, prompt: promptMessage({ id: 'b', at: T0 + 1 }) }).queue,
    { now: T0, prompt: promptMessage({ id: 'a', at: T0 + 2 }) },
  ).queue;
  assert.deepEqual(
    pending(two, { now: T0 }).pending.map((entry) => entry.prompt.id),
    ['b', 'a'],
  );
});

// ---------------------------------------------------------------------------
// The count across accounts (§3.2's "Two questions waiting")
// ---------------------------------------------------------------------------

test('waitingSentence says the count, and nothing at zero', () => {
  assert.equal(waitingSentence(0), null, 'an announcement of nothing is noise');
  assert.equal(waitingSentence(1), 'One question waiting');
  assert.equal(waitingSentence(2), 'Two questions waiting');
  assert.equal(waitingSentence(3), 'Three questions waiting');
  assert.equal(waitingSentence(12), 'Twelve questions waiting');
  assert.equal(waitingSentence(13), '13 questions waiting');
  assert.equal(waitingSentence(-1), null);
  assert.equal(waitingSentence(1.5), null);
});

function twoAccounts() {
  const first = enqueue(
    enqueue(makeQueue({ account: 'acct-a' }), { now: T0, prompt: promptMessage({ id: 'p1' }) }).queue,
    { now: T0, prompt: promptMessage({ id: 'p2', at: T0 + 1 }) },
  ).queue;
  const second = enqueue(
    makeQueue({ account: 'acct-b' }),
    { now: T0, prompt: promptMessage({ id: 'q1', sender: 'agent-2' }) },
  ).queue;
  return [first, second];
}

test('a natural pause counts every account\'s waiting prompts in one sentence', () => {
  const announced = announcement(twoAccounts(), { now: T0 + 2 * MINUTE });
  assert.equal(announced.ok, true);
  assert.equal(announced.count, 3);
  assert.equal(announced.text, 'Three questions waiting');
  // The due list keeps each prompt with its account, so the app can say which
  // installation is asking before it reads the question out.
  assert.deepEqual(announced.due.map((entry) => [entry.account, entry.prompt.id]), [
    ['acct-a', 'p1'],
    ['acct-a', 'p2'],
    ['acct-b', 'q1'],
  ]);
});

test('the two-question sentence of the spec, from two installations', () => {
  const queues = [
    enqueue(makeQueue({ account: 'acct-a' }), { now: T0, prompt: promptMessage() }).queue,
    enqueue(makeQueue({ account: 'acct-b' }), {
      now: T0,
      prompt: promptMessage({ id: 'q1', sender: 'agent-2' }),
    }).queue,
  ];
  assert.equal(announcement(queues, { now: T0 + MINUTE }).text, 'Two questions waiting');
});

test('what\'s pending is grouped per installation, and never summed', () => {
  const across = pendingAcross(twoAccounts(), { now: T0 + 2 * MINUTE });
  assert.equal(across.ok, true);
  assert.equal(across.count, 3);
  assert.deepEqual(across.accounts.map((group) => group.account), ['acct-a', 'acct-b']);
  assert.deepEqual(across.accounts.map((group) => group.pending.length), [2, 1]);
  assert.deepEqual(across.accounts[0].pending.map((entry) => entry.prompt.id), ['p1', 'p2']);
  // No summed total per installation: D170 shows one row per installation and
  // no total, and the grouping is what makes that expressible.
  assert.equal(Object.hasOwn(across.accounts[0], 'count'), false);
});

test('the cross-account view refuses two queues for one installation', () => {
  const queues = twoAccounts();
  const duplicated = [queues[0], queues[0]];
  assert.equal(announcement(duplicated, { now: T0 }).code, REFUSAL.INVALID_QUEUES);
  assert.equal(pendingAcross(duplicated, { now: T0 }).code, REFUSAL.INVALID_QUEUES);
  assert.equal(announcement('nope', { now: T0 }).code, REFUSAL.INVALID_QUEUES);
  assert.equal(announcement(queues, { now: T0 - 1 }).code, REFUSAL.NOW_WENT_BACKWARDS);
  // A clock that is not carried at all is missing-field here as it is on every
  // other entry point: the two faults have two fixes, and the module answers
  // one code for one fault wherever it is entered.
  assert.equal(announcement(queues, {}).code, REFUSAL.MISSING_FIELD);
  assert.equal(announcement(queues, {}).field, 'now');
  assert.equal(pendingAcross(queues, {}).code, REFUSAL.MISSING_FIELD);
  assert.equal(announcement(queues, { now: 0 }).code, REFUSAL.INVALID_NOW);
  assert.equal(pendingAcross(queues, { now: 'x' }).code, REFUSAL.INVALID_NOW);
  // The cross-account view is not a bag either: an event with a field it does
  // not read is refused rather than read with the field dropped.
  assert.equal(announcement(queues, { now: T0, at: T0 }).code, REFUSAL.UNKNOWN_FIELD);
  assert.equal(pendingAcross(queues, { now: T0, at: T0 }).code, REFUSAL.UNKNOWN_FIELD);
  assert.equal(announcement([], { now: T0 }).text, null, 'no installations is no questions');
});

// ---------------------------------------------------------------------------
// The interrupt policy (D160, D127)
// ---------------------------------------------------------------------------

const digestOf = (severity) => ({ severity, items: [] });

function policy(over = {}) {
  return evaluateInterrupt({ queue: makeQueue(), now: T0, ...over });
}

test('only D160\'s set interrupts, and the switch decides which part of it', () => {
  // The whole table, cell by cell: every event under every switch setting, so
  // no cell is left undecided for a caller to interpret.
  const events = [
    ['nothing', {}, null],
    ['kill', { digest: digestOf(SEVERITY.kill) }, INTERRUPT.KILL],
    ['cap reached', { caps: { reached: true } }, INTERRUPT.CAP_REACHED],
    ['cap near', { caps: { near: true } }, INTERRUPT.CAP_NEAR],
    [
      'going offline',
      { presence: { state: PRESENCE.OFFLINE, previous: PRESENCE.LIVE } },
      INTERRUPT.OFFLINE,
    ],
  ];
  const expected = {
    [SWITCH.NARROW]: {
      nothing: null,
      kill: INTERRUPT.KILL,
      'cap reached': INTERRUPT.CAP_REACHED,
      'cap near': null,
      'going offline': null,
    },
    [SWITCH.DEFAULT]: {
      nothing: null,
      kill: INTERRUPT.KILL,
      'cap reached': INTERRUPT.CAP_REACHED,
      'cap near': INTERRUPT.CAP_NEAR,
      'going offline': INTERRUPT.OFFLINE,
    },
    [SWITCH.WIDEN]: {
      nothing: null,
      kill: INTERRUPT.KILL,
      'cap reached': INTERRUPT.CAP_REACHED,
      'cap near': INTERRUPT.CAP_NEAR,
      'going offline': INTERRUPT.OFFLINE,
    },
  };
  const reasons = new Set();
  for (const [name, input, reason] of events) {
    for (const setting of [SWITCH.NARROW, SWITCH.DEFAULT, SWITCH.WIDEN]) {
      const result = policy({ ...input, switch: setting });
      assert.equal(result.ok, true, JSON.stringify(result));
      assert.equal(
        result.reason,
        expected[setting][name],
        `${name} under ${setting} should answer ${expected[setting][name]}`,
      );
      assert.equal(result.interrupt, reason !== null && expected[setting][name] !== null);
      assert.equal(result.interrupt, result.reason !== null, 'the flag and the reason agree');
      if (result.reason !== null) reasons.add(result.reason);
    }
  }
  // `widen`'s fifth cell is its own test below; it is the last reason this
  // vocabulary has to account for.
  reasons.add(policy({ queue: enqueueOne(makeQueue()).queue, switch: SWITCH.WIDEN }).reason);
  // The vocabulary is exactly what the policy answers, both ways round: every
  // reason it returns is a member of `INTERRUPT` (a caller renders a string
  // this module defined), and every member is reachable (a member nothing
  // returns is a reason a caller would wait for for ever).
  assert.deepEqual([...reasons].sort(), [...Object.values(INTERRUPT)].sort());
  assert.equal(Object.values(INTERRUPT).includes('loud'), false, 'and nothing off the list is on it');
});

test('widening adds "any prompt", and only when one is due', () => {
  const fresh = enqueueOne(makeQueue()).queue;
  assert.equal(policy({ queue: fresh, switch: SWITCH.WIDEN }).reason, INTERRUPT.PROMPT);
  assert.equal(policy({ queue: fresh, switch: SWITCH.WIDEN, switch2: 1 }).ok, false);
  // The default set does NOT include a waiting prompt: widening is the only
  // setting under which a question of its own is a reason to speak over
  // someone, and off-by-default is what keeps the queue quiet.
  assert.equal(policy({ queue: fresh, switch: SWITCH.DEFAULT }).reason, null);
  assert.equal(policy({ queue: fresh, switch: SWITCH.NARROW }).reason, null);
  let queue = fresh;

  // A prompt already spoken is not a reason to speak over the user again: what
  // widening adds is a question the user has not heard.
  queue = announce(queue, { now: T0 + MINUTE }).queue;
  const spoken = policy({ queue, switch: SWITCH.WIDEN, now: T0 + MINUTE });
  assert.equal(spoken.ok, true);
  assert.equal(spoken.reason, null);
  assert.equal(policy({ queue, switch: SWITCH.DEFAULT, now: T0 + MINUTE }).reason, null);
});

test('the loudest event is the one reported', () => {
  const all = policy({
    digest: digestOf(SEVERITY.kill),
    caps: { reached: true, near: true },
    presence: { state: PRESENCE.OFFLINE, previous: PRESENCE.LIVE },
    switch: SWITCH.WIDEN,
  });
  assert.equal(all.reason, INTERRUPT.KILL);
  assert.equal(
    policy({ caps: { reached: true, near: true }, switch: SWITCH.WIDEN }).reason,
    INTERRUPT.CAP_REACHED,
  );
  assert.equal(
    policy({ caps: { near: true }, presence: { state: PRESENCE.OFFLINE, previous: PRESENCE.LIVE } })
      .reason,
    INTERRUPT.CAP_NEAR,
  );
});

test('an offline first sighting is not a transition, and does not interrupt', () => {
  // D160's event is "going from live to offline". A consumer the app has never
  // seen alive has not gone anywhere, and interrupting on it would make the
  // app speak over the user for an installation that was never up.
  for (const presence of [
    { state: PRESENCE.OFFLINE },
    { state: PRESENCE.OFFLINE, previous: null },
    { state: PRESENCE.STALE, previous: PRESENCE.LIVE },
    { state: PRESENCE.CLOCK_DIFFERS, previous: PRESENCE.LIVE },
  ]) {
    const result = policy({ presence });
    assert.equal(result.ok, true, JSON.stringify(result));
    assert.equal(result.reason, null, `${JSON.stringify(presence)} must not interrupt`);
  }
  // Any other state to offline is a transition, including leaving clock-differs.
  assert.equal(
    policy({ presence: { state: PRESENCE.OFFLINE, previous: PRESENCE.CLOCK_DIFFERS } }).reason,
    INTERRUPT.OFFLINE,
  );
  // Already offline is not a new transition.
  assert.equal(
    policy({ presence: { state: PRESENCE.OFFLINE, previous: PRESENCE.OFFLINE } }).reason,
    null,
  );
});

test('the policy refuses what it cannot read, and never guesses a severity', () => {
  assert.equal(policy({ switch: 'loud' }).code, REFUSAL.INVALID_SWITCH);
  assert.equal(policy({ digest: digestOf('catastrophic') }).code, REFUSAL.INVALID_SEVERITY);
  assert.equal(policy({ digest: digestOf('catastrophic') }).field, 'digest.severity');
  assert.equal(policy({ digest: {} }).code, REFUSAL.INVALID_SEVERITY);
  assert.equal(policy({ digest: 'kill' }).code, REFUSAL.INVALID_SEVERITY);
  assert.equal(policy({ presence: { state: 'fine' } }).code, REFUSAL.INVALID_PRESENCE);
  assert.equal(policy({ presence: { state: PRESENCE.LIVE, previous: 'fine' } }).code, REFUSAL.INVALID_PRESENCE);
  assert.equal(policy({ caps: { reached: 'yes' } }).code, REFUSAL.INVALID_CAPS);
  assert.equal(policy({ caps: { plenty: true } }).code, REFUSAL.INVALID_CAPS);
  assert.equal(policy({ now: T0 - 1 }).code, REFUSAL.NOW_WENT_BACKWARDS);
  assert.equal(policy({ now: 0 }).code, REFUSAL.INVALID_NOW);
  assert.equal(policy({ extra: 1 }).code, REFUSAL.UNKNOWN_FIELD);
  // The clock: not carried is missing-field, carried and unusable is
  // invalid-now, exactly as on every other entry point (the `policy` helper
  // injects `now`, so this is the one place the fault is reachable).
  const untimed = evaluateInterrupt({ queue: makeQueue() });
  assert.equal(untimed.code, REFUSAL.MISSING_FIELD);
  assert.equal(untimed.field, 'now');
  // A signal that is absent and a signal that is null mean the same thing --
  // nothing to report -- for all three of the policy's inputs, and a value that
  // is neither is still refused rather than read as "no event".
  assert.equal(policy({ digest: null }).ok, true);
  assert.equal(policy({ caps: null }).ok, true, 'null caps are no caps, as null presence is no presence');
  assert.equal(policy({ caps: null }).interrupt, false);
  assert.equal(policy({ presence: null }).ok, true);
  assert.equal(evaluateInterrupt({ queue: makeQueue(), now: T0, caps: null, digest: null, presence: null }).ok, true);
  // A severity below the top rung is not an interruption, and every rung of
  // P1.3's ladder is readable.
  for (const severity of Object.values(SEVERITY)) {
    const result = policy({ digest: digestOf(severity) });
    assert.equal(result.ok, true, `${severity} must be readable`);
    assert.equal(result.reason, severity === SEVERITY.kill ? INTERRUPT.KILL : null);
  }
});

test('the policy is a pure function of its inputs', () => {
  const queue = enqueueOne(makeQueue()).queue;
  const input = {
    queue,
    digest: digestOf(SEVERITY.failed),
    presence: { state: PRESENCE.LIVE, previous: PRESENCE.LIVE },
    caps: { reached: false, near: false },
    switch: SWITCH.DEFAULT,
    now: T0 + MINUTE,
  };
  const first = evaluateInterrupt(input);
  const second = evaluateInterrupt({ ...input, queue: { ...queue } });
  assert.deepEqual(first, second);
  assert.equal(first.pending, 1);
  assert.equal(first.interrupt, false);
  // The reason is null when nothing interrupts. When something does, it is one
  // of `INTERRUPT`'s members and nothing else: the table test above collects
  // every reason the module answers across the whole (event x switch) table and
  // asserts that set IS `Object.values(INTERRUPT)`, so a caller rendering a
  // reason is rendering a string this module defines -- and no member of the
  // vocabulary is a reason nothing returns.
  assert.equal(first.reason, null);
  assert.equal(policy({ digest: digestOf(SEVERITY.kill) }).reason, INTERRUPT.KILL);
});

test('the queue module holds no clock: an unchanged input is unchanged output', () => {
  // Every entry point takes its `now`, so two identical calls are identical
  // without a frozen clock anywhere. This is what makes P6.7's consumer able
  // to replay a decision and D126's purity auditable.
  const queue = enqueueOne(makeQueue()).queue;
  const first = announcement([queue], { now: T0 + HOUR });
  const second = announcement([queue], { now: T0 + HOUR });
  assert.deepEqual(first, second);
  assert.equal(first.count, second.count);
});

test('every object the module hands back is frozen, entry point by entry point', () => {
  // The header's rule ("every answer and every refusal is frozen"), walked
  // rather than sampled. A caller may hold any of these results and keep it in
  // its own state, and a value it could rewrite is a value this module's next
  // call could not predict -- so the claim has to be over EVERY entry point,
  // not the handful an earlier version of this test happened to name. Two of
  // that version's five asserts could never fail. `Object.isFrozen(first.due)`
  // was on `announcement`'s `due`, which is an array `announcement` already
  // freezes -- so it was redundant with the code it was checking, not evidence
  // of anything. `Object.isFrozen(isExpired(T0, T0, HOUR))` was on a boolean,
  // and a primitive is frozen whatever this module does. (It is worth saying
  // which `due`: `evaluateInterrupt` answers a `due` too, but it is a boolean
  // and no assertion was ever made on it.) The rule is about the objects -- and
  // walking the surface is what found the one object that was not frozen,
  // `initialQueue`'s result, which is now frozen like the rest and pinned by
  // its own guard in `mutation-proof.sh`.
  const queue = enqueueOne(makeQueue()).queue;

  /** Every object at or under `value`, four levels down, must be frozen. */
  const walk = (value, where, depth) => {
    if (value === null || typeof value !== 'object') return;
    assert.ok(Object.isFrozen(value), `${where} is frozen`);
    if (depth === 0) return;
    for (const [key, inner] of Object.entries(value)) {
      walk(inner, `${where}.${key}`, depth - 1);
    }
  };

  const answers = [
    ['initialQueue', () => initialQueue({ now: T0, account: 'acct-a' })],
    ['enqueue (queued)', () => enqueue(queue, { now: T0, prompt: promptMessage({ id: 'p2' }) })],
    ['expire', () => expire(queue, { now: T0 + PROMPT_TTL_MS + 1 })],
    ['answer', () => answer(queue, { now: T0, answer: answerMessage() })],
    ['pending', () => pending(queue, { now: T0 })],
    ['announce', () => announce(queue, { now: T0 })],
    ['pendingAcross', () => pendingAcross([queue], { now: T0 })],
    ['announcement', () => announcement([queue], { now: T0 })],
    ['evaluateInterrupt', () => evaluateInterrupt({ queue, now: T0, switch: SWITCH.WIDEN })],
  ];
  for (const [name, call] of answers) {
    const result = call();
    assert.equal(result.ok, true, `${name}: ${JSON.stringify(result)}`);
    walk(result, name, 4);
  }

  const refusals = [
    ['initialQueue', () => initialQueue({ now: T0 })],
    ['enqueue (id-conflict)', () => enqueue(queue, { now: T0 + MINUTE, prompt: promptMessage({ id: 'p1', at: T0 + MINUTE }) })],
    ['expire (backwards)', () => expire(queue, { now: T0 - 1 })],
    ['answer (unknown prompt)', () => answer(queue, { now: T0, answer: answerMessage({ reply_to: 'nope' }) })],
    ['pending (backwards)', () => pending(queue, { now: T0 - 1 })],
    ['announce (backwards)', () => announce(queue, { now: T0 - 1 })],
    ['pendingAcross (not a list)', () => pendingAcross(queue, { now: T0 })],
    ['announcement (two queues, one account)', () => announcement([queue, { ...queue }], { now: T0 })],
    ['evaluateInterrupt (bad switch)', () => evaluateInterrupt({ queue, now: T0, switch: 'shout' })],
  ];
  for (const [name, call] of refusals) {
    const result = call();
    assert.equal(result.ok, false, `${name}: ${JSON.stringify(result)}`);
    walk(result, name, 4);
  }

  // Four entry points answer a bare value rather than a result, and a primitive
  // is frozen whatever this module does: asserted for the shape of the answer,
  // not for a freeze that could never fail.
  assert.equal(typeof isExpired(T0, T0, HOUR), 'boolean');
  assert.equal(typeof reminderStage(T0, T0), 'number');
  assert.equal(typeof waitingSentence(2), 'string');
  assert.equal(typeof acceptanceKind(false), 'string');
  assert.ok(Object.isFrozen(policy({})));
});

test('the module is a pure function: no clock, no file, no network, no node: builtin', () => {
  // The module header and the README both claim this, and a claim nothing
  // checks is a comment. What it claims is D126's rule and this package's
  // contract: nothing here reads a clock, a file, a database, a network or a
  // random source -- every entry point takes its `now` -- and the module imports
  // nothing outside `shared/` and no `node:` builtin, because the phone is one
  // of the two consumers and React Native's bundler resolves none of them
  // (P0.2's entry point is guarded the same way, for the same reason).
  const source = readFileSync(new URL('./index.js', import.meta.url), 'utf8');

  const specifiers = [...source.matchAll(/^\s*import\s[^;]*?from\s*['"]([^'"]+)['"]/gm)].map(
    (match) => match[1],
  );
  assert.ok(specifiers.length > 0, 'the scan must actually find this module\'s imports');
  for (const specifier of specifiers) {
    assert.match(
      specifier,
      /^\.\.?\//,
      `shared/prompts/index.js imports ${specifier}, which is not a relative path`,
    );
    // Resolved, not merely read: `../../../etc/passwd` is relative and is not
    // inside this package.
    const resolved = new URL(specifier, import.meta.url).pathname;
    assert.ok(
      resolved.includes('/shared/'),
      `shared/prompts/index.js imports ${specifier}, which resolves outside shared/`,
    );
  }

  const forbidden = [
    [/\brequire\s*\(/, 'require()'],
    [/new\s+Date\b/, 'a clock of its own'],
    [/Date\s*\.\s*now/, 'a clock of its own'],
    [/Math\s*\.\s*random/, 'a random source'],
    [/\bperformance\s*\./, 'a clock of its own'],
    [/\bfetch\s*\(/, 'a network client'],
    [/\bsetTimeout\s*\(/, 'a timer'],
    [/\bsetInterval\s*\(/, 'a timer'],
    [/\bprocess\s*\./, 'a process handle'],
    [/\bglobalThis\b/, 'a global handle'],
    [/node:/, 'a node: builtin'],
    [/\bBuffer\b/, 'a Node buffer type'],
  ];
  for (const [pattern, what] of forbidden) {
    assert.equal(pattern.test(source), false, `shared/prompts/index.js must not reach for ${what}`);
  }
});

test('a queue handed back mutated is refused, not trusted', () => {
  const queue = enqueueOne(makeQueue()).queue;
  // A queue is read field by field and its prompts re-validated: a caller that
  // hands one back with a field removed, an extra key, a bad TTL, an
  // out-of-order list or a duplicated id is refused rather than answered from.
  const broken = [
    [{ ...queue, prompts: 'nope' }, REFUSAL.INVALID_PROMPTS],
    [{ ...queue, promptTtlMs: 1 }, REFUSAL.INVALID_QUEUE],
    [{ ...queue, maxPending: 0 }, REFUSAL.INVALID_QUEUE],
    [{ ...queue, account: '' }, REFUSAL.INVALID_QUEUE],
    [{ ...queue, nope: 1 }, REFUSAL.UNKNOWN_FIELD],
    [{ ...queue, futureToleranceMs: MAX_FUTURE_TOLERANCE_MS + 1 }, REFUSAL.INVALID_QUEUE],
    [{ ...queue, prompts: [queue.prompts[0], queue.prompts[0]] }, REFUSAL.DUPLICATE_ENTRY],
    [{ ...queue, prompts: [{ ...queue.prompts[0], prompt: { ...queue.prompts[0].prompt, mac: 'zz' } }] }, REFUSAL.INVALID_ENTRY],
    [{ ...queue, prompts: [{ ...queue.prompts[0], destructive: 'yes' }] }, REFUSAL.INVALID_ENTRY],
    [{ ...queue, prompts: [{ ...queue.prompts[0], extra: 1 }] }, REFUSAL.INVALID_ENTRY],
  ];
  for (const [value, code] of broken) {
    const result = expire(value, { now: queue.now });
    assert.equal(result.ok, false, `expected a refusal for ${JSON.stringify(value)}`);
    assert.equal(result.code, code);
  }
  // Two entries out of canonical order are refused: the order is part of what
  // a queue is, exactly as P1.1's ledger order is.
  const two = enqueue(
    enqueue(makeQueue(), { now: T0, prompt: promptMessage({ id: 'a', at: T0 }) }).queue,
    { now: T0, prompt: promptMessage({ id: 'b', at: T0 + 1 }) },
  ).queue;
  assert.equal(
    expire({ ...two, prompts: [two.prompts[1], two.prompts[0]] }, { now: T0 }).code,
    REFUSAL.UNSORTED_PROMPTS,
  );
  // More prompts than the cap allows is a queue no producer could have made.
  assert.equal(
    expire({ ...queue, maxPending: 0 }, { now: T0 }).code,
    REFUSAL.INVALID_QUEUE,
  );
});

// ---------------------------------------------------------------------------
// Hostile inputs: a refusal, never an exception (the package's convention)
// ---------------------------------------------------------------------------

/** A name the module must never repeat back: whatever the trap was handed. */
const SECRET = 'a name this module must not repeat';

/** A proxy whose every trap throws SECRET. */
function hostile() {
  const trap = () => {
    throw new Error(SECRET);
  };
  return new Proxy({}, {
    getPrototypeOf: trap,
    ownKeys: trap,
    getOwnPropertyDescriptor: trap,
    get: trap,
    has: trap,
  });
}

/** A revoked proxy: every trap throws, and `IsArray` throws too. */
function revoked() {
  const held = Proxy.revocable({}, {});
  held.revoke();
  return held.proxy;
}

test('an exception from a hostile input is a refusal, and what was thrown is never echoed', () => {
  // P1.1 and P6.2 both answer a frozen `internal-error` refusal where an input
  // makes them throw, and document it; a hostile value is an input like any
  // other. This module is reachable from the same places -- a queue is local
  // state the app hands back, and a digest, a message and a switch come from
  // elsewhere -- so it answers the same way, and what was thrown is never
  // echoed: it may carry whatever the input put in it, including a key.
  const queue = enqueueOne(makeQueue()).queue;
  const calls = [
    ['initialQueue(options)', () => initialQueue(hostile())],
    ['initialQueue(revoked)', () => initialQueue(revoked())],
    ['enqueue(queue)', () => enqueue(hostile(), { now: T0, prompt: promptMessage() })],
    ['enqueue(revoked queue)', () => enqueue(revoked(), { now: T0, prompt: promptMessage() })],
    ['enqueue(event)', () => enqueue(queue, hostile())],
    ['enqueue(revoked event)', () => enqueue(queue, revoked())],
    ['expire(event)', () => expire(queue, hostile())],
    ['expire(revoked queue)', () => expire(revoked(), { now: T0 })],
    ['answer(event)', () => answer(queue, hostile())],
    ['answer(revoked queue)', () => answer(revoked(), { now: T0, answer: answerMessage() })],
    ['pending(queue)', () => pending(hostile(), { now: T0 })],
    ['announce(event)', () => announce(queue, revoked())],
    ['pendingAcross(queues)', () => pendingAcross([hostile()], { now: T0 })],
    ['pendingAcross(revoked)', () => pendingAcross(revoked(), { now: T0 })],
    ['announcement(queues)', () => announcement([hostile()], { now: T0 })],
    ['announcement(revoked)', () => announcement(revoked(), { now: T0 })],
    ['evaluateInterrupt(input)', () => evaluateInterrupt(hostile())],
    ['evaluateInterrupt(revoked)', () => evaluateInterrupt(revoked())],
    // A queue whose own entries are hostile: the throw happens deeper in, in
    // the reader that re-validates each held prompt.
    ['pending(a hostile entry)', () => pending({ ...queue, prompts: [hostile()] }, { now: T0 })],
    ['evaluateInterrupt(hostile caps)', () => evaluateInterrupt({ queue, now: T0, caps: hostile() })],
  ];
  // The two predicates are named here because they are NOT in the list: each
  // checks its arguments first and answers the argument's own code
  // (`invalid-at`/`invalid-now`/`invalid-ttl`, their own tests), which is the
  // same fail-closed answer reached one step earlier -- and never `false` or
  // `NaN`, the two answers a caller cannot tell from a real one.
  assert.equal(isExpired(revoked(), T0, HOUR).code, REFUSAL.INVALID_AT);
  assert.equal(reminderStage(revoked(), T0).code, REFUSAL.INVALID_AT);

  for (const [what, call] of calls) {
    let result;
    try {
      result = call();
    } catch (error) {
      assert.fail(`${what} threw instead of refusing: ${error && error.message}`);
    }
    assert.equal(result.ok, false, what);
    assert.equal(result.code, REFUSAL.INTERNAL_ERROR, what);
    assert.equal(result.field, null, what);
    assert.ok(Object.isFrozen(result), what);
    assert.equal(JSON.stringify(result).includes(SECRET), false, `${what}: ${JSON.stringify(result)}`);
  }
});

test('no entry point throws, whatever it is handed', () => {
  // The same claim over the WHOLE exported surface, including the helpers that
  // answer a bare value rather than a result: a caller can hold a function of
  // this module and a value from anywhere, and neither the exception nor the
  // module's header should be a surprise.
  const queue = enqueueOne(makeQueue()).queue;
  const surface = {
    initialQueue: () => initialQueue(revoked()),
    enqueue: () => enqueue(revoked(), revoked()),
    expire: () => expire(revoked(), revoked()),
    answer: () => answer(revoked(), revoked()),
    pending: () => pending(revoked(), revoked()),
    announce: () => announce(revoked(), revoked()),
    pendingAcross: () => pendingAcross(revoked(), { now: T0 }),
    announcement: () => announcement(revoked(), { now: T0 }),
    evaluateInterrupt: () => evaluateInterrupt(revoked()),
    isExpired: () => isExpired(hostile(), hostile(), hostile()),
    reminderStage: () => reminderStage(hostile(), hostile()),
    waitingSentence: () => waitingSentence(hostile()),
    acceptanceKind: () => acceptanceKind(hostile()),
  };
  for (const [name, call] of Object.entries(surface)) {
    assert.doesNotThrow(call, `${name} must not throw`);
  }
  // The two that cannot throw are asserted to be what they always were: a
  // hostile count is not a sentence, and a hostile flag is still a flag.
  assert.equal(waitingSentence(revoked()), null);
  assert.equal(acceptanceKind(revoked()), MAILBOX_KIND.COMMAND);
  assert.equal(queue.prompts.length, 1, 'and none of this touched the queue');
});

test('reminderStage is monotone in `at`, which is why the listing is the queue order', () => {
  // The premise behind two things that are one thing: escalation is monotone in
  // a prompt's `at` (an older prompt has waited at least as long), and the queue
  // is stored `at`,id-ordered -- so listing the most escalated first IS listing
  // the oldest first, and `pending` needs no stage rule of its own. Asserted
  // here rather than assumed, because a rule that cannot differ from the order
  // it sits on is a rule nothing defends.
  let previous = -Infinity;
  for (let offset = 0; offset <= 5 * DAY; offset += 17 * MINUTE) {
    const stage = reminderStage(T0 - offset, T0);
    assert.ok(
      stage >= previous,
      `stage(${offset} ms old) = ${stage} must not be lower than stage(${offset - 17 * MINUTE} ms old) = ${previous}`,
    );
    previous = stage;
  }
});

test('expire reports the deaths oldest first, which is the order they are held in', () => {
  // No sort, and none needed: the queue is stored `at`,id-ordered, the expired
  // list is built by walking it, and the listing order is that order (previous
  // test). An orchestrator waiting the longest hears first.
  const queue = enqueue(
    enqueue(
      enqueue(makeQueue({ promptTtlMs: HOUR }), { now: T0, prompt: promptMessage({ id: 'p1' }) }).queue,
      { now: T0, prompt: promptMessage({ id: 'p2', at: T0 + MINUTE }) },
    ).queue,
    { now: T0, prompt: promptMessage({ id: 'p3', at: T0 + 2 * MINUTE }) },
  ).queue;
  const swept = expire(queue, { now: T0 + 2 * HOUR });
  assert.equal(swept.ok, true, JSON.stringify(swept));
  assert.deepEqual(swept.expired.map((entry) => entry.id), ['p1', 'p2', 'p3']);
  assert.deepEqual(swept.expired.map((entry) => entry.at), [T0, T0 + MINUTE, T0 + 2 * MINUTE]);
});

test('the digest is read through its own descriptor: an accessor is refused, not called', () => {
  // `severity` is the one field of P1.3's digest the policy reads, and it is
  // read like every other field this module reads -- once, through its own
  // descriptor. A getter here would be called and believed, which is the one
  // thing the module's whole answer is not allowed to do: it decides whether
  // the app may speak over the user.
  let called = 0;
  const digest = { project: 'fugaro', total: 1, items: [] };
  Object.defineProperty(digest, 'severity', {
    enumerable: true,
    get() {
      called += 1;
      return SEVERITY.kill;
    },
  });
  const queue = enqueueOne(makeQueue()).queue;
  const result = evaluateInterrupt({ queue, digest, switch: SWITCH.DEFAULT, now: T0 });
  assert.equal(result.ok, false, JSON.stringify(result));
  assert.equal(result.code, REFUSAL.ACCESSOR_PROPERTY);
  assert.equal(result.field, 'digest.severity');
  assert.equal(called, 0, 'the getter must not be called: a value read twice is not a value');

  // And the field the module does read is still read: P1.3's own digest shape
  // (project, total, severity, items) is accepted whole.
  assert.equal(
    evaluateInterrupt({ queue, digest: digestOf(SEVERITY.kill), switch: SWITCH.DEFAULT, now: T0 }).reason,
    INTERRUPT.KILL,
  );
  assert.equal(
    evaluateInterrupt({ queue, digest: digestOf(SEVERITY.info), switch: SWITCH.DEFAULT, now: T0 }).interrupt,
    false,
  );
});
