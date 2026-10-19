// The prompt queue and the interrupt policy (mobile plan, Task P6.5).
//
// Two pure answers over the same input, one about holding questions and one
// about when to speak:
//
//   * the QUEUE -- prompts an agent asked that nobody has answered yet: it
//     holds them, announces how many are waiting at a natural pause, lists them
//     per installation, re-announces the ones that have waited too long, expires
//     them at D118's mailbox TTL and refuses the one that arrives at the cap;
//   * the INTERRUPT POLICY -- whether anything may be spoken over the user at
//     all, which is D160's set of four events read through D127's local switch.
//
// Why the queue exists at all: the spec's own default is **queue, do not
// interrupt** ("Prompts are held rather than interrupting the user", §3.2), and
// the tree had nothing that held anything -- Voice D48 dropped "awaiting user
// input" because no producer existed (G7), and the merged mailbox modules
// (P0.2's schema, P1.1's state machine, P1.3's digest, P1.4's staleness rule)
// decide what a message IS, whether a delivery is a replay, what is severe and
// whether the orchestrator is alive -- none of them holds a question for later.
// This module is that holder, and it is deliberately the only one: P6.7's
// consumer and P6.9's text screen both speak what this module lists.
//
// ## Prompts expire at the TTL (D168 as amended, O4, O5, D173)
//
// The owner first answered "never expire", then amended it the same day: a
// prompt's lifetime is **D118's mailbox TTL**, which the owner set to **72
// hours** (O5, 2026-10-17, inside P1.1's thirty-day ceiling for a prompt's TTL,
// `MAX_PROMPT_TTL_MS` -- not the seven days P1.1 caps the six-hour command
// window's `windowMs` at). So
//
//     expired(prompt, now)  ==  now - prompt.at > promptTtlMs
//
// a pure function of the prompt's own `at`, the TTL and the caller's clock --
// **not** of when a sweeper last ran, and not of the phone's clock, which D117
// refuses to trust. Expiry YIELDS something rather than merely dropping a
// question: the prompt's **non-destructive answer**, or a stop when it has none
// (`EXPIRED_REPLY`), because the waiting orchestrator session must be told
// something and D168 says the default is always the non-destructive option.
// Exactly-once delivery of that answer is the consumer's (P6.7), ledger-
// protected; this module only decides WHAT the answer is.
//
// **The reads are over a swept queue, and that is the caller's ordering.** A
// held prompt dies while it waits: nothing here kills it on a timer, because
// `expire` is what kills it and the consumer calls it on its own clock (D168 as
// amended). So `pending`, `pendingAcross`, `announce`, `announcement` and
// `evaluateInterrupt` apply **no TTL**: an unswept dead prompt is still listed
// (in the caller's own queue and in every installation's row of the
// cross-account view, D170), still due, still counted and still able to widen
// the interrupt. That is deliberate, not a
// gap -- a listing that quietly omitted a dead prompt would destroy the answer
// the orchestrator is owed, which is the one thing this module refuses to do
// anywhere else (a full queue is refused, never evicted) -- and P6.7 expires
// before it reads. `index.test.js` pins both halves: what a read reports when
// the caller has not swept, and that the sweep clears it.
//
// The boundary is the same boundary P1.1 applies, and that is not a coincidence
// to be re-derived: P1.1 accepts a prompt of age exactly the TTL
// (`now - at > window` refuses) and P6.5 expires it at exactly the same line,
// so an answer and an expiry can never disagree about whether a prompt is
// alive. `index.test.js` asserts the agreement against P1.1's own `step`
// rather than restating the arithmetic, which is what keeps the two modules
// from drifting.
//
// ## Two windows, and which one an answer takes (D173, §3.6, O6)
//
// The owner ruled that the **six-hour replay window applies to commands**
// (cancel, merge, launch, confirm) and a **prompt answer takes the prompt's
// TTL** -- so a consumer offline longer than six hours but inside the TTL still
// accepts a queued prompt -- **except** that an answer to a **destructive**
// prompt is a confirmation and takes the command window, because a
// confirmation must be fresh. That is D169's shape: a destructive answer is an
// action record, not free text -- and §3.5 makes it the **only** shape one is
// accepted in, so `answer` refuses a plain `answer` to a destructive prompt
// rather than passing P6.7 a command it must then refuse. The MAC over that
// record is P6.7's to verify; the shape is this module's.
//
// This module does not re-implement the windows: it **passes the kind** to
// P1.1's kind-parameterised API (`KIND.COMMAND` or `KIND.PROMPT`, the same
// vocabulary P1.1's `deliver` reads) and `acceptanceKind` is the mapping. What
// is checked here is the other half, which P1.1 cannot check: whether the
// PROMPT is still alive, and whether a confirmation's own time -- §3.5 checks
// the command window from the confirmation time, not from the envelope's `at`
// -- is inside the command window.
//
// ## Reminder escalation bounds attention; the cap bounds storage (§3.4)
//
// Escalation is **not** an expiry, and the owner kept it when D168 was
// amended. A waiting prompt is announced at the next natural pause, then
// re-announced after one hour and once a day after that, and it is listed first
// in "what's pending". The schedule is anchored to the prompt's `at` --
// `reminderStage` is a pure function of `at` and `now` -- while whether it has
// actually been SPOKEN is phone-local state (`announcedAt`), because the app
// announces at pauses and a pure clock function cannot know when those were.
//
// The hard cap is a separate bound, per consumer, because storage is per
// installation: a queue at its maximum refuses the next prompt with a defined
// answer to the orchestrator (`QUEUE_FULL_REPLY`, "queue full"), and the
// disclosure is the point -- an orchestrator whose question was refused must
// not wait for an answer that will never come.
//
// ## What interrupts (D160, D127)
//
// D160 fixed the set: a kill switch set, a daily cap reached, a cap near (at a
// local threshold, off by default) and the active installation's consumer going
// from live to offline; everything else queues. The switch D127 answered is
// local -- never written to the database -- and it can only **narrow** the set
// or **widen** it to "any prompt". Which members survive narrowing is P6.5's
// own choice, recorded in `SWITCH` and pinned by an exhaustive table test,
// because D160 fixes the set and not the subset.
//
// The policy is a pure function of the queue, the digest (P1.3's result: `kill`
// is the ladder's top rung), the presence rule (P1.4's state, plus the state
// the app last showed, because "going offline" is a transition), the caps'
// recomputation (the phone cannot see per-run spend or halted runs -- tension
// (b1) -- so P3.2/P3.3 compute the fraction and this module decides what it
// means) and the switch. It reads no clock: `now` is an input on every call.
//
// Nothing here reads a clock, a file, the network or a random source, and the
// only imports are the merged pure modules this task builds on: P1.1's windows
// and kind vocabulary, P1.3's severity ladder, P1.4's state names and P6.3's
// schema. Nothing here raises either: every entry point is wrapped in `guarded`
// (see below), so a hostile value -- a revoked Proxy, or one whose traps throw
// -- is answered as the frozen `internal-error` refusal, the same code P1.1 and
// P6.2 answer for the same reason.
// `shared/prompts/README.md` is the module's own contract, and
// `../../docs/mobile/implementation-plan.md`'s P6.5 is this module's task.

import { severityRank, SEVERITY } from '../digest/severity.js';
import {
  DEFAULT_FUTURE_TOLERANCE_MS,
  DEFAULT_WINDOW_MS,
  KIND,
  MAX_FORWARD_JUMP_MS,
  MAX_FUTURE_TOLERANCE_MS,
  MAX_PROMPT_TTL_MS,
  MAX_WINDOW_MS,
  MIN_WINDOW_MS,
  TIME_MAX,
} from '../mailbox/index.js';
import { FIELDS as MESSAGE_FIELDS, OPTION_MAX, validateMessage } from '../mailbox-schema/index.js';
import { STATE as PRESENCE } from '../presence/index.js';

/**
 * The mailbox TTL, in milliseconds: **72 hours**, which the owner set on
 * 2026-10-17 (O5) for D118's mailbox TTL and for the unanswered-prompt TTL
 * alike. It is this module's default and not a constant it imposes: every
 * entry point takes `promptTtlMs`, exactly as P1.1's state does, so an operator
 * who moves the sweeper's window moves this with it. The number is here
 * because P6.5 has to have one to be a default at all, and because the owner's
 * answer is a ruling rather than a measurement.
 */
export const PROMPT_TTL_MS = 72 * 60 * 60 * 1_000;

/** The TTL's own bounds: P1.1's floor, and its `MAX_PROMPT_TTL_MS` ceiling. */
export const MIN_TTL_MS = MIN_WINDOW_MS;
export const MAX_TTL_MS = MAX_PROMPT_TTL_MS;

/**
 * How far one event's `now` may lead the queue's own watermark: **366 days**,
 * which is P1.1's bound for the same leap (`MAX_FORWARD_JUMP_MS`, imported
 * rather than restated, so the mailbox and the queue cannot disagree about how
 * far a clock may jump).
 *
 * `now` is monotone here: it is written into the queue and every later reading
 * earlier than it is refused `now-went-backwards`. Without this bound, one bad
 * clock reading -- or one hostile caller -- writing `TIME_MAX` as the watermark
 * would refuse every honest `now` after it for ever: the queue bricked by a
 * single event, on a phone whose user cannot fix it. A leap past the bound is
 * refused `now-jumped` and the watermark stays where it was, so the next honest
 * reading is accepted. A consumer that really was off for longer starts from a
 * new `initialQueue`, which is exactly what P1.1 says for its own state; by
 * then every prompt this queue held is past its TTL anyway (72 hours).
 */
export const MAX_JUMP_MS = MAX_FORWARD_JUMP_MS;

/**
 * When a waiting prompt is re-announced: at the next natural pause after one
 * hour, and once a day after that (§3.4's bounded alternative, kept when D168
 * was amended). Anchored to the prompt's `at`, so the schedule is a function of
 * the clock and not of how many pauses happened.
 */
export const REMINDER_AFTER_MS = 60 * 60 * 1_000;
export const REMINDER_INTERVAL_MS = 24 * 60 * 60 * 1_000;

/**
 * How many prompts one consumer may hold pending at once. A bounded default an
 * operator can move, not a measurement: D118 bounds one message's size, this
 * bounds how many questions an installation can accumulate while nobody
 * answers, and the refusal (`queue-full`) is loud rather than silent because
 * the orchestrator is the party that would otherwise wait for ever.
 */
export const DEFAULT_MAX_PENDING = 16;
export const MIN_MAX_PENDING = 1;
export const MAX_MAX_PENDING = 1_024;

/**
 * What the orchestrator is told when its prompt is refused at the cap. D168
 * asks for "a defined answer to the orchestrator" and this is it: short, fixed,
 * and not a promise that the question will be asked later, because it is
 * dropped rather than held.
 */
export const QUEUE_FULL_REPLY = 'queue full';

/**
 * What the orchestrator is told when a prompt expires with no non-destructive
 * answer to give it. D168's own words for it: "no answer, expired" -- the
 * conversation stops, and it stops knowing why.
 */
export const EXPIRED_REPLY = 'no answer, expired';

/**
 * D127's local switch. It never reaches the database (the owner's answer says
 * why: it is a per-device preference, and storing it would cost another phone
 * write and grant), and it does exactly two things to D160's set:
 *
 *   narrow   only the two events that mean the work or the money has stopped:
 *            a kill switch set and a daily cap reached. The offline transition
 *            is a state the app shows, not a reason to speak over someone.
 *   default  D160's set as answered: kill, cap reached, cap near (when the
 *            operator has turned the local threshold on) and the transition to
 *            offline.
 *   widen    the default set plus "any prompt": a prompt that is due to be read
 *            is spoken at once instead of waiting for the next pause.
 */
export const SWITCH = Object.freeze({
  NARROW: 'narrow',
  DEFAULT: 'default',
  WIDEN: 'widen',
});

/**
 * The reasons the policy can interrupt with. A reason is not a boolean: the app
 * says different things for a kill switch and for a lost orchestrator, and a
 * caller that renders an unlisted string is rendering something this module
 * never returns.
 */
export const INTERRUPT = Object.freeze({
  KILL: 'kill',
  CAP_REACHED: 'cap-reached',
  CAP_NEAR: 'cap-near',
  OFFLINE: 'offline',
  PROMPT: 'prompt',
});

/**
 * What a queue operation did with a delivery. `expired` is an outcome and not a
 * refusal on purpose: the prompt arrived past its TTL, which is a fact about
 * the prompt and not a malformed input, and the caller needs the expiry's
 * answer (the safe one, or the stop) in the same breath -- refusing would drop
 * the very thing the orchestrator is owed.
 */
export const OUTCOME = Object.freeze({
  QUEUED: 'queued',
  DUPLICATE: 'duplicate',
  EXPIRED: 'expired',
  ANSWERED: 'answered',
});

/**
 * The refusal codes. One per distinct way an input can fail to be this module's
 * input, because "the queue is full" and "the queue was handed something it
 * could not read" are different facts and one code for both loses the second
 * one. The schema's own codes are propagated with a `prompt.` prefix (a stored
 * prompt is re-validated by P6.3's validator, so the two cannot disagree about
 * what a prompt is).
 */
export const REFUSAL = Object.freeze({
  INPUT_NOT_AN_OBJECT: 'input-not-an-object',
  UNKNOWN_FIELD: 'unknown-field',
  // A field that is carried and not read, and a field that is not carried at
  // all: two faults with two fixes, spelled as P1.1 and P6.2 spell them.
  MISSING_FIELD: 'missing-field',
  ACCESSOR_PROPERTY: 'accessor-property',
  INTERNAL_ERROR: 'internal-error',

  INVALID_AT: 'invalid-at',
  INVALID_NOW: 'invalid-now',
  NOW_WENT_BACKWARDS: 'now-went-backwards',
  // The clock's other fault, and P1.1's code for it: a leap forward past
  // `MAX_JUMP_MS`, refused without being written, so the watermark holds and
  // the next honest reading is accepted.
  NOW_JUMPED: 'now-jumped',
  INVALID_ACCOUNT: 'invalid-account',
  INVALID_TTL: 'invalid-ttl',
  INVALID_WINDOW: 'invalid-window',
  INVALID_MAX_PENDING: 'invalid-max-pending',
  INVALID_FUTURE_TOLERANCE: 'invalid-future-tolerance',

  INVALID_QUEUE: 'invalid-queue',
  INVALID_PROMPTS: 'invalid-prompts',
  INVALID_ENTRY: 'invalid-entry',
  DUPLICATE_ENTRY: 'duplicate-entry',
  UNSORTED_PROMPTS: 'unsorted-prompts',
  INVALID_QUEUES: 'invalid-queues',

  // The delivery.
  NOT_A_PROMPT: 'not-a-prompt',
  NOT_AN_ANSWER: 'not-an-answer',
  // §3.5's shape rule, the destructive half of the same question: an answer to
  // a destructive prompt is accepted ONLY as an action record carrying a
  // confirmation, never as a plain `answer` -- which is what the consumer must
  // refuse -- and a confirmation answers only a destructive prompt.
  NOT_A_CONFIRMATION: 'not-a-confirmation',
  INVALID_DESTRUCTIVE: 'invalid-destructive',
  INVALID_SAFE: 'invalid-safe',
  SAFE_NOT_AN_OPTION: 'safe-not-an-option',
  ZERO_TIMESTAMP: 'zero-timestamp',
  NEGATIVE_ZERO: 'negative-zero',
  FUTURE_TIMESTAMP: 'future-timestamp',
  EXPIRED: 'expired',
  REPLAY: 'replay',
  ID_CONFLICT: 'id-conflict',
  QUEUE_FULL: 'queue-full',

  // The answer.
  UNKNOWN_PROMPT: 'unknown-prompt',

  // The policy.
  INVALID_SWITCH: 'invalid-switch',
  INVALID_SEVERITY: 'invalid-severity',
  INVALID_PRESENCE: 'invalid-presence',
  INVALID_CAPS: 'invalid-caps',
});

/** Which states P1.4's rule can answer with, read from P1.4 rather than restated. */
const PRESENCE_STATES = Object.freeze(Object.values(PRESENCE));

/** The switch vocabulary, for the one check that is not a table lookup. */
const SWITCH_VALUES = Object.freeze(Object.values(SWITCH));

/**
 * The queue's own keys. An unknown one is refused: a queue is not a bag, and a
 * queue from a later protocol version must be refused rather than read with the
 * new field dropped.
 */
const QUEUE_FIELDS = Object.freeze([
  'account',
  'now',
  'promptTtlMs',
  'windowMs',
  'maxPending',
  'futureToleranceMs',
  'prompts',
]);

/** An entry's keys, exactly: the prompt, its local classification and its announcement. */
const ENTRY_FIELDS = Object.freeze(['prompt', 'destructive', 'safe', 'announcedAt']);

/**
 * A plain data object -- an object literal, or one with no prototype -- and not
 * an instance of anything. The check is here rather than implied by the field
 * checks because an instance can answer a field through a getter, which is a
 * value this module cannot read once and be sure of.
 */
function isPlainObject(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return false;
  }
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

/**
 * An integer inside a closed range, and never a negative zero: `-0` is the
 * shape of a number that has been negated, which is a computation's residue
 * rather than a time or a count. P1.1's rule, applied to the same kinds of
 * value.
 */
function isIntegerInRange(value, min, max) {
  return (
    typeof value === 'number' &&
    Number.isSafeInteger(value) &&
    !Object.is(value, -0) &&
    value >= min &&
    value <= max
  );
}

/** A refusal: no queue, a code the caller can branch on, and the field at fault. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * checkStamp judges one epoch-millisecond reading against every bound this
 * module puts on a time, most specific fault first: `-0` and an unset counter
 * are their own codes (P6.3's schema accepts both), then the module's own
 * ceiling, then P1.1's future tolerance. Returns a refusal, or `null` when the
 * reading is one this module will carry.
 *
 * The ceiling is not decoration. `now` may be `TIME_MAX` and the tolerance
 * reaches past it, so a write path holding only the tolerance could store a
 * timestamp the READ path refuses -- and then every later call on that queue
 * would come back `invalid-entry`: a queue bricked one call over, which is the
 * failure the `now-jumped` refusal exists to prevent on the clock's side. The
 * writer and the reader are held to the same bound because they are the same
 * bound, spelled once.
 */
function checkStamp(value, now, toleranceMs, where) {
  if (Object.is(value, -0)) return refuse(REFUSAL.NEGATIVE_ZERO, where);
  if (value < 1) return refuse(REFUSAL.ZERO_TIMESTAMP, where);
  if (value > TIME_MAX) return refuse(REFUSAL.FUTURE_TIMESTAMP, where);
  if (value - now > toleranceMs) return refuse(REFUSAL.FUTURE_TIMESTAMP, where);
  return null;
}

/**
 * guarded makes an entry point total: every exported function below is wrapped
 * in it, so a hostile value -- a revoked Proxy, or one whose traps throw -- is
 * answered as the frozen `internal-error` refusal instead of raising out of a
 * module whose whole contract is to decide, not to fail.
 *
 * The reason it is one function rather than a copy per entry point is this
 * module's own: two copies of one rule are two behaviours waiting to differ.
 * P1.1 (`shared/mailbox/index.js`, `step` and `initialState`) and P6.2
 * (`shared/confirm/index.js`, `initialState`, `step`, `deliver`) answer the same
 * code for the same reason, and the code is spelled the same, so a caller can
 * branch on either module's answer.
 *
 * What was thrown is **never echoed** and nothing of the exception is read:
 * the value carries whatever the input put in it, which on this module's inputs
 * could be a name out of a key or a document, and a refusal is not a place to
 * repeat it. `field` is therefore `null`: which field was at fault is exactly
 * what could not be determined.
 */
function guarded(fn) {
  return function (...args) {
    try {
      return fn(...args);
    } catch {
      return refuse(REFUSAL.INTERNAL_ERROR, null);
    }
  };
}

/**
 * readFields reads exactly the named fields into a null-prototype snapshot,
 * each field once, through its own descriptor.
 *
 * A value that is not a plain data object is refused (`not-an-object`), a key
 * the caller does not name -- a symbol included -- is refused (`unknown-field`),
 * and an accessor property is refused (`accessor-property`) without ever being
 * called: a getter can answer differently each time it is read, and this
 * module's whole claim is that its answer is a function of the value it was
 * handed. The accessor has its OWN code rather than sharing `unknown-field`,
 * because the field is one this module names and knows -- the caller's mistake
 * is the property's shape, not its key -- and it is spelled exactly as P1.1
 * spells it, so one caller can branch on either module's answer. This is P1.1's
 * reader, at P6.5's size.
 */
function readFields(value, names, where) {
  if (!isPlainObject(value)) return refuse(REFUSAL.INPUT_NOT_AN_OBJECT, where);

  const values = Object.create(null);
  for (const key of Reflect.ownKeys(value)) {
    if (!names.includes(key)) return refuse(REFUSAL.UNKNOWN_FIELD, where);
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (descriptor === undefined || !Object.hasOwn(descriptor, 'value')) {
      return refuse(REFUSAL.ACCESSOR_PROPERTY, key);
    }
    values[key] = descriptor.value;
  }
  return { ok: true, values };
}

// ---------------------------------------------------------------------------
// The clock's two rules
// ---------------------------------------------------------------------------

/**
 * Whether a prompt written at `at` has expired by `now`, given the TTL. A pure
 * function of three values, exported because it is the rule and not an
 * implementation detail: P6.7's consumer applies it to prompts it holds
 * locally, and the test that pins it against P1.1's acceptance boundary is what
 * keeps the two modules agreeing.
 *
 * Strictly greater: a prompt of age exactly the TTL is still alive, which is
 * exactly where P1.1's `deliver` stops accepting it (`now - at > window`).
 *
 * Every argument is checked before any arithmetic happens, and a value that is
 * not the argument it belongs in is refused rather than coerced -- `invalid-at`
 * for `at`, `invalid-now` for `now`, `invalid-ttl` for the TTL, the codes this
 * module's queue-level entry points already answer for those three arguments.
 * `false` would be the other possible answer, and it is the one a predicate
 * that cannot run must not give: `false` says "still alive", which would hand
 * the user a question the TTL has already ended. Subtraction gives it freely --
 * `now - at > ttl` is false for `undefined`, `'x'` and `NaN`, and TRUE for
 * `null`, which is not a number either -- so the check is what makes the
 * promise hold, and a hostile Proxy in a number's place is refused with the
 * same code rather than being read as one.
 *
 * Guarded like every other entry point (see `guarded`) as well: the checks are
 * a type test and so cannot raise on any value, and the guard is there for the
 * reason it is everywhere -- a caller may hold this function and a value from
 * anywhere.
 */
export const isExpired = guarded(isExpiredUnguarded);

function isExpiredUnguarded(at, now, promptTtlMs) {
  if (!isIntegerInRange(at, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_AT, 'at');
  if (!isIntegerInRange(now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');
  if (!isIntegerInRange(promptTtlMs, MIN_TTL_MS, MAX_TTL_MS)) {
    return refuse(REFUSAL.INVALID_TTL, 'promptTtlMs');
  }
  return expiredBy(at, now, promptTtlMs);
}

/**
 * The rule itself, over three values a caller has already validated: the walk
 * over a queue's own prompts calls this rather than `isExpired`, because a
 * refusal is a truthy object and `if (isExpired(...))` would read one as "this
 * prompt is dead". `readQueue` is what makes the three values this function's.
 */
function expiredBy(at, now, promptTtlMs) {
  return now - at > promptTtlMs;
}

/**
 * How many times a prompt waiting since `at` has come due to be re-announced by
 * `now`: 0 before the first hour, 1 from one hour on, 2 from one hour and a day
 * on, and so on. A pure function of the clock -- the schedule is anchored to
 * the prompt's own arrival -- while whether it has actually been SPOKEN is the
 * entry's local `announcedAt`, because this module cannot know when the app's
 * natural pauses were.
 *
 * Its two arguments are checked like `isExpired`'s, and for the same reason: a
 * stage is a number the text screen renders and this module compares, and the
 * arithmetic would otherwise answer one for a value that is not a time --
 * `NaN`, which is neither 0 nor any stage, and which sorts and compares as
 * nothing at all. `invalid-at` for `at`, `invalid-now` for `now`, both bounded
 * `1..TIME_MAX` as everywhere else, and a hostile Proxy in a number's place is
 * refused with the same code.
 */
export const reminderStage = guarded(reminderStageUnguarded);

function reminderStageUnguarded(at, now) {
  if (!isIntegerInRange(at, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_AT, 'at');
  if (!isIntegerInRange(now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');
  return stageOf(at, now);
}

/**
 * The schedule itself, over two values a caller has already validated: the
 * queue's own walk calls this rather than `reminderStage`, so a refusal object
 * -- truthy, and not a stage -- can never reach the comparison that decides
 * whether a question is read out.
 */
function stageOf(at, now) {
  const waited = now - at;
  if (waited < REMINDER_AFTER_MS) return 0;
  return 1 + Math.floor((waited - REMINDER_AFTER_MS) / REMINDER_INTERVAL_MS);
}

/** The instant a stage's announcement came due: `at + 1h + (stage - 1) days`. */
function reminderAt(at, stage) {
  return at + REMINDER_AFTER_MS + (stage - 1) * REMINDER_INTERVAL_MS;
}

/**
 * Whether a prompt is due to be read out at `now`: never announced, or the
 * announcement of its current stage's reminder has not happened yet. An
 * announcement made after a reminder's due time counts as that reminder's, so a
 * prompt announced late is not announced twice for one stage.
 */
// What is due: never announced, or this stage's reminder not yet spoken.
//
// The stage-0 branch is not redundant, and an earlier version of this module
// removed it on the argument that the fall-through already answers false --
// that at stage 0 the reminder time is `at - 23h`, which no announcement can
// precede. The argument does not hold against this module's own bounds: a
// prompt may be written up to `futureToleranceMs` AHEAD of the clock (P1.1's
// 24-hour ceiling, and a hand-built queue may carry any `announcedAt`), and for
// an `at` between 23 and 24 hours ahead the stage-0 reminder time is still in
// the future, so the fall-through would report the question due at every pause
// and `announce` would re-speak it for ever. Stage 0 means one announcement,
// its own; the hour has to pass before anything is due again.
function isDue(entry, now) {
  if (entry.announcedAt === null) return true;
  const stage = stageOf(entry.prompt.at, now);
  if (stage === 0) return false;
  return entry.announcedAt < reminderAt(entry.prompt.at, stage);
}

// ---------------------------------------------------------------------------
// The queue
// ---------------------------------------------------------------------------

/**
 * initialState-style constructor for one consumer's queue:
 *
 *     initialQueue({ now, account, promptTtlMs?, windowMs?, maxPending?, futureToleranceMs? })
 *
 * `now` is required and is the caller's, anchored to the server (D117).
 * `account` is required: the installation (D114) this queue belongs to, which
 * is what makes the cross-account announcement a count and the "what's
 * pending" listing per installation. `promptTtlMs` defaults to the owner's
 * 72 hours (O5); `windowMs` defaults to P1.1's command window, imported rather
 * than restated, because an answer to a destructive prompt takes it (D173).
 *
 * It returns a frozen `{ ok: true, queue }` or a refusal; it never throws — a
 * hostile value that makes it raise is answered as `internal-error`, like every
 * other entry point here (`guarded`).
 *
 * @param {{ now: number, account: string, promptTtlMs?: number, windowMs?: number,
 *           maxPending?: number, futureToleranceMs?: number }} [options]
 * @returns {{ ok: true, queue: object } | { ok: false, code: string, field: string | null }}
 */
export const initialQueue = guarded(initialQueueUnguarded);

function initialQueueUnguarded(options = {}) {
  const read = readFields(
    options,
    QUEUE_FIELDS.filter((field) => field !== 'prompts'),
    'options',
  );
  if (!read.ok) return read;
  const values = read.values;

  const now = values.now;
  if (now === undefined) return refuse(REFUSAL.MISSING_FIELD, 'now');
  if (!isIntegerInRange(now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');

  const account = values.account;
  if (account === undefined) return refuse(REFUSAL.MISSING_FIELD, 'account');
  if (typeof account !== 'string' || account === '' || account.length > 64) {
    return refuse(REFUSAL.INVALID_ACCOUNT, 'account');
  }

  const promptTtlMs = values.promptTtlMs === undefined ? PROMPT_TTL_MS : values.promptTtlMs;
  if (!isIntegerInRange(promptTtlMs, MIN_TTL_MS, MAX_TTL_MS)) {
    return refuse(REFUSAL.INVALID_TTL, 'promptTtlMs');
  }

  const windowMs = values.windowMs === undefined ? DEFAULT_WINDOW_MS : values.windowMs;
  if (!isIntegerInRange(windowMs, MIN_WINDOW_MS, MAX_WINDOW_MS)) {
    return refuse(REFUSAL.INVALID_WINDOW, 'windowMs');
  }

  const maxPending = values.maxPending === undefined ? DEFAULT_MAX_PENDING : values.maxPending;
  if (!isIntegerInRange(maxPending, MIN_MAX_PENDING, MAX_MAX_PENDING)) {
    return refuse(REFUSAL.INVALID_MAX_PENDING, 'maxPending');
  }

  const futureToleranceMs =
    values.futureToleranceMs === undefined ? DEFAULT_FUTURE_TOLERANCE_MS : values.futureToleranceMs;
  // P1.1's ceiling, imported rather than restated: this is the bound that says
  // how far a prompt's `at` may lead the clock, and the reminder schedule is
  // read off the same clock (see `isDue`), so the two must not be free to drift.
  if (!isIntegerInRange(futureToleranceMs, 0, MAX_FUTURE_TOLERANCE_MS)) {
    return refuse(REFUSAL.INVALID_FUTURE_TOLERANCE, 'futureToleranceMs');
  }

  // Frozen like every other result an entry point hands back: the queue is a
  // value, and the object carrying it is one too. A caller that could rewrite
  // `ok`, or swap the queue on the result it was given, would be a caller whose
  // next call this module could not predict -- and this constructor was the one
  // exception.
  return Object.freeze({
    ok: true,
    queue: freezeQueue({
      account,
      now,
      promptTtlMs,
      windowMs,
      maxPending,
      futureToleranceMs,
      prompts: [],
    }),
  });
}

/** A queue value, frozen at every level: entries and their prompts alike. */
function freezeQueue(queue) {
  for (const entry of queue.prompts) Object.freeze(entry);
  Object.freeze(queue.prompts);
  return Object.freeze(queue);
}

/**
 * readQueue reads a queue into a frozen snapshot, or refuses.
 *
 * Every field is read exactly once and checked, the prompts are re-validated by
 * P6.3's `validateMessage` (so a queue cannot hold a message the schema would
 * refuse, whatever a caller did to it), and the entries must be in canonical
 * order with no id twice. A queue is not trusted because it looks like one: it
 * is the value a caller hands back, and this module's answers are a function of
 * it.
 */
function readQueue(value, where) {
  const read = readFields(value, QUEUE_FIELDS, where);
  if (!read.ok) return read;
  const values = read.values;

  for (const field of QUEUE_FIELDS) {
    if (values[field] === undefined) return refuse(REFUSAL.INVALID_QUEUE, field);
  }
  if (typeof values.account !== 'string' || values.account === '' || values.account.length > 64) {
    return refuse(REFUSAL.INVALID_QUEUE, 'account');
  }
  if (!isIntegerInRange(values.now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_QUEUE, 'now');
  if (!isIntegerInRange(values.promptTtlMs, MIN_TTL_MS, MAX_TTL_MS)) {
    return refuse(REFUSAL.INVALID_QUEUE, 'promptTtlMs');
  }
  if (!isIntegerInRange(values.windowMs, MIN_WINDOW_MS, MAX_WINDOW_MS)) {
    return refuse(REFUSAL.INVALID_QUEUE, 'windowMs');
  }
  if (!isIntegerInRange(values.maxPending, MIN_MAX_PENDING, MAX_MAX_PENDING)) {
    return refuse(REFUSAL.INVALID_QUEUE, 'maxPending');
  }
  if (!isIntegerInRange(values.futureToleranceMs, 0, MAX_FUTURE_TOLERANCE_MS)) {
    return refuse(REFUSAL.INVALID_QUEUE, 'futureToleranceMs');
  }
  if (!Array.isArray(values.prompts) || values.prompts.length > values.maxPending) {
    return refuse(REFUSAL.INVALID_PROMPTS, 'prompts');
  }

  const entries = [];
  for (const raw of values.prompts) {
    const entry = readEntry(raw);
    if (!entry.ok) return entry;
    entries.push(entry.entry);
  }
  const ids = new Set();
  for (const entry of entries) {
    const id = entry.prompt.id;
    if (ids.has(id)) return refuse(REFUSAL.DUPLICATE_ENTRY, 'prompts');
    ids.add(id);
  }
  for (let index = 1; index < entries.length; index += 1) {
    if (compareEntries(entries[index - 1], entries[index]) >= 0) {
      return refuse(REFUSAL.UNSORTED_PROMPTS, 'prompts');
    }
  }

  return {
    ok: true,
    queue: freezeQueue({
      account: values.account,
      now: values.now,
      promptTtlMs: values.promptTtlMs,
      windowMs: values.windowMs,
      maxPending: values.maxPending,
      futureToleranceMs: values.futureToleranceMs,
      prompts: entries,
    }),
  };
}

/**
 * readEntry reads one stored entry: the prompt (re-validated), its local
 * classification (`destructive`, `safe`) and its announcement time.
 *
 * The two local fields are **not** part of the message, and the schema is what
 * makes that structural rather than a convention: `validateMessage` refuses any
 * field it does not name, so a classification cannot be smuggled into the
 * bytes, and `canonicalBytes` -- what the MAC covers -- never sees it. D127's
 * reasoning is the same one: per-device state stays on the device.
 */
function readEntry(raw) {
  const read = readFields(raw, ENTRY_FIELDS, 'prompts');
  if (!read.ok) return refuse(REFUSAL.INVALID_ENTRY, 'prompts');
  const entry = read.values;
  if (!('prompt' in entry) || !('destructive' in entry) || !('safe' in entry) || !('announcedAt' in entry)) {
    return refuse(REFUSAL.INVALID_ENTRY, 'prompts');
  }

  const validated = validateMessage(entry.prompt);
  if (!validated.ok) return refuse(REFUSAL.INVALID_ENTRY, 'prompts');
  const prompt = validated.message;
  if (prompt.kind !== KIND_OF_PROMPT) return refuse(REFUSAL.INVALID_ENTRY, 'prompts');
  // The schema accepts `at: 0` and `at: -0` (both are numerically zero) and any
  // safe integer up to `Number.MAX_SAFE_INTEGER`, and this module carries
  // neither: `enqueue` refuses the first two as `zero-timestamp` and
  // `negative-zero`, and a value past the ceiling as `future-timestamp`. A
  // queue is a value the caller hands back, so the reader applies the same
  // bound rather than trusting a stored prompt the writer could never have
  // sent -- otherwise an entry with `at: 0` would be "expired" from the moment
  // it was written, and one at the ceiling would be a prompt no age bounds.
  if (!isIntegerInRange(prompt.at, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_ENTRY, 'prompts');

  if (typeof entry.destructive !== 'boolean') return refuse(REFUSAL.INVALID_ENTRY, 'prompts');
  const safe = readSafe(entry.safe, prompt);
  if (safe.bad !== undefined) return refuse(REFUSAL.INVALID_ENTRY, 'prompts');
  if (entry.announcedAt !== null && !isIntegerInRange(entry.announcedAt, 1, TIME_MAX)) {
    return refuse(REFUSAL.INVALID_ENTRY, 'prompts');
  }

  return {
    ok: true,
    entry: {
      prompt,
      destructive: entry.destructive,
      safe: safe.value,
      announcedAt: entry.announcedAt,
    },
  };
}

/** The `kind` a queued prompt must carry: P6.3's `prompt`, and nothing else. */
const KIND_OF_PROMPT = 'prompt';

/**
 * readSafe checks a prompt's non-destructive answer: `null` when the prompt has
 * none, or a bounded single-line string. When the prompt carries `options`, a
 * safe answer that is NOT one of them is refused -- D168's default is "the
 * non-destructive option", and an answer the user could never have chosen is
 * not an option at all, so handing it to the orchestrator would be this module
 * inventing one.
 */
function readSafe(value, prompt) {
  if (value === null || value === undefined) return { value: null };
  if (typeof value !== 'string' || value === '') return { bad: REFUSAL.INVALID_SAFE };
  if (value.length > OPTION_MAX) return { bad: REFUSAL.INVALID_SAFE };
  for (let i = 0; i < value.length; i += 1) {
    const code = value.charCodeAt(i);
    if (code < 0x20 || code === 0x7f) return { bad: REFUSAL.INVALID_SAFE };
  }
  if (Array.isArray(prompt.options) && !prompt.options.includes(value)) {
    return { bad: REFUSAL.SAFE_NOT_AN_OPTION };
  }
  return { value };
}

/**
 * The queue's canonical order: `at`, then the message id, compared by code
 * unit. `at` first, because it is the order the questions were asked in; the id
 * only to make the order total, so two prompts asked in the same millisecond
 * cannot leave two different queues depending on which arrived first. A
 * frozen order is what makes two deliveries in either order identical, which is
 * P1.1's argument for its own ledger and is needed here for the same reason.
 */
function compareEntries(left, right) {
  if (left.prompt.at !== right.prompt.at) return left.prompt.at < right.prompt.at ? -1 : 1;
  if (left.prompt.id !== right.prompt.id) return left.prompt.id < right.prompt.id ? -1 : 1;
  return 0;
}

/** insert keeps the queue's prompts in canonical order, without a sort. */
function insert(entries, entry) {
  let index = 0;
  while (index < entries.length && compareEntries(entries[index], entry) < 0) index += 1;
  return [...entries.slice(0, index), entry, ...entries.slice(index)];
}

/** The event's own keys, exactly. */
const DELIVERY_FIELDS = Object.freeze(['now', 'prompt', 'destructive', 'safe']);
const ANSWER_EVENT_FIELDS = Object.freeze(['now', 'answer']);
const TIME_ONLY_FIELDS = Object.freeze(['now']);

/**
 * A queue operation's common prologue: read the queue, read the event's own
 * `now`, and refuse a clock that went backwards.
 *
 * A `now` earlier than the queue's own is refused rather than absorbed
 * (`now-went-backwards`), because expiry is a function of `now`: a clock that
 * could run in reverse would make an expired prompt alive again and re-arm a
 * reminder that was already spoken. A leap FORWARD past `MAX_JUMP_MS` is
 * refused too (`now-jumped`, P1.1's code for the same leap): the `now` this
 * module accepts is written into the queue as its watermark, so an unbounded
 * forward jump would let one bad reading or one hostile caller put the watermark
 * at `TIME_MAX` and refuse every honest reading after it for ever. The refusal
 * leaves the queue exactly as it was -- nothing is written on the way out -- so
 * the caller's next honest `now` is accepted.
 */
function open(queue, event, names, where) {
  const current = readQueue(queue, 'queue');
  if (!current.ok) return current;
  const read = readFields(event, names, where);
  if (!read.ok) return read;
  if (read.values.now === undefined) return refuse(REFUSAL.MISSING_FIELD, 'now');
  if (!isIntegerInRange(read.values.now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');
  if (read.values.now < current.queue.now) return refuse(REFUSAL.NOW_WENT_BACKWARDS, 'now');
  if (read.values.now - current.queue.now > MAX_JUMP_MS) {
    return refuse(REFUSAL.NOW_JUMPED, 'now');
  }
  return { ok: true, queue: current.queue, values: read.values };
}

/**
 * The clock alone, read from an event that carries nothing else: the two
 * cross-account reads take `{ now }` and no queue, and they answer a missing
 * or unusable `now` exactly as `open` does -- one fault, one code, wherever the
 * module is entered (`missing-field` when it is not carried at all,
 * `invalid-now` when it is carried and is not a time).
 */
function readClock(event, where) {
  const time = readFields(event, TIME_ONLY_FIELDS, where);
  if (!time.ok) return time;
  const now = time.values.now;
  if (now === undefined) return refuse(REFUSAL.MISSING_FIELD, 'now');
  if (!isIntegerInRange(now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');
  return time;
}

/**
 * enqueue adds a prompt the agent asked:
 *
 *     enqueue(queue, { now, prompt, destructive?, safe? })
 *
 * `prompt` is a validated P6.3 message of kind `prompt` (the consumer's outbox
 * message, MAC and all -- this module has no key and verifies nothing: P3.6
 * verifies the outbox on the phone, and the rules only require the field to be
 * present). `destructive` says its answer is a confirmation (D169/§3.6) and
 * defaults to false; `safe` is its non-destructive answer, or omitted when the
 * prompt has none.
 *
 * The default is the CALLER's obligation, not this module's safety net: whether
 * a prompt's effect is destructive is phone-local and never signed (D127), so
 * nothing on the wire says it and this module cannot tell a misclassified
 * prompt from a quiet one. P6.7 -- the only writer -- passes the flag
 * explicitly for every prompt it queues, and P6.7's own card (§3.5) is what
 * refuses an answer to a destructive prompt in any other shape. `false` is for
 * a caller that has established there is nothing to flag.
 *
 * Outcomes, and why each is not the other:
 *
 *   queued      accepted, in canonical order.
 *   duplicate   the same id, sender and `at` is already held: answered, not
 *               re-armed, so a redelivery cannot clear an announcement or
 *               reset a clock.
 *   expired     the prompt arrived past its TTL. Not a refusal: the lifetime
 *               ended, and the caller is handed the expiry's answer (the safe
 *               one, or the stop) so the orchestrator is not left waiting.
 *
 * Refusals: `queue-full` at the cap (the one refusal that carries a defined
 * answer to the orchestrator), `id-conflict` for a held id under a different
 * sender or time, `not-a-prompt`, `safe-not-an-option`, the future and zero
 * timestamp bounds, and every code P6.3's validator raises, with the field it
 * names prefixed `prompt.` when the schema defines that field, and the prompt
 * reported as a whole when it does not -- an unknown key is the writer's name,
 * not this module's (P1.1's rule, from the schema's own `FIELDS`).
 *
 * @param {object} queue a queue, as returned by initialQueue or an operation
 * @param {{ now: number, prompt: object, destructive?: boolean, safe?: string }} event
 * @returns {object} `{ ok: true, outcome, queue, ... }` or a refusal
 */
export const enqueue = guarded(enqueueUnguarded);

function enqueueUnguarded(queue, event) {
  const opened = open(queue, event, DELIVERY_FIELDS, 'event');
  if (!opened.ok) return opened;
  const { queue: current, values } = opened;

  const validated = validateMessage(values.prompt);
  if (!validated.ok) {
    // The schema names a field by its key, and an UNKNOWN field's key is the
    // writer's: it is not a name this module knows, and echoing it would carry
    // whatever it holds out of here in an answer nothing read it into. Only a
    // field the schema itself defines is named, and anything else is reported
    // as the prompt as a whole -- P1.1's rule, from P1.1's own import
    // (`FIELDS`), for the same reason.
    return refuse(
      validated.code,
      MESSAGE_FIELDS.includes(validated.field) ? `prompt.${validated.field}` : 'prompt',
    );
  }
  const prompt = validated.message;
  if (prompt.kind !== KIND_OF_PROMPT) return refuse(REFUSAL.NOT_A_PROMPT, 'prompt.kind');

  // Every bound this module puts on a time, the ceiling among them: `at: 0` is
  // schema-valid and is not a time this module will carry (the TTL would be
  // measured from an unset counter, so the prompt would be "expired" from the
  // moment it was written -- P1.1 refuses the same value for the same reason),
  // and a prompt past `TIME_MAX` is one `readEntry` refuses, so writing it
  // would leave a queue no later call can read.
  const stamp = checkStamp(prompt.at, values.now, current.futureToleranceMs, 'prompt.at');
  if (stamp !== null) return stamp;

  const destructive = values.destructive === undefined ? false : values.destructive;
  if (typeof destructive !== 'boolean') return refuse(REFUSAL.INVALID_DESTRUCTIVE, 'destructive');
  const safe = readSafe(values.safe, prompt);
  if (safe.bad) return refuse(safe.bad, 'safe');

  const held = current.prompts.find((entry) => entry.prompt.id === prompt.id);
  if (held !== undefined) {
    if (held.prompt.sender === prompt.sender && held.prompt.at === prompt.at) {
      return Object.freeze({ ok: true, outcome: OUTCOME.DUPLICATE, id: prompt.id, queue: current });
    }
    return refuse(REFUSAL.ID_CONFLICT, 'prompt.id');
  }

  if (expiredBy(prompt.at, values.now, current.promptTtlMs)) {
    // The answer the orchestrator is owed, handed back with the outcome: the
    // caller (P6.7) delivers it once and records that it did.
    return Object.freeze({
      ok: true,
      outcome: OUTCOME.EXPIRED,
      id: prompt.id,
      queue: current,
      expiry: expiryOf({ prompt, destructive, safe: safe.value }),
    });
  }

  if (current.prompts.length >= current.maxPending) {
    // Refused, never evicted: dropping a question to make room would answer
    // nothing and lose the one that was dropped. The refusal carries
    // D168's defined answer so the orchestrator learns it must not wait.
    return Object.freeze({
      ok: false,
      code: REFUSAL.QUEUE_FULL,
      field: null,
      reply: QUEUE_FULL_REPLY,
      maxPending: current.maxPending,
    });
  }

  const entry = Object.freeze({
    prompt,
    destructive,
    safe: safe.value,
    announcedAt: null,
  });
  return Object.freeze({
    ok: true,
    outcome: OUTCOME.QUEUED,
    id: prompt.id,
    queue: freezeQueue({
      ...current,
      now: values.now,
      prompts: insert(current.prompts, entry),
    }),
  });
}

/**
 * expiryOf is what a prompt's death hands the orchestrator: the prompt's
 * non-destructive answer, or a stop when the prompt has none (D168 as amended).
 * One function, because `expire` and a delivery that arrives already dead must
 * answer identically -- two copies of this rule would be two defaults for one
 * conversation.
 */
function expiryOf(entry) {
  return Object.freeze({
    id: entry.prompt.id,
    at: entry.prompt.at,
    destructive: entry.destructive,
    safe: entry.safe,
    stop: entry.safe === null,
    reply: entry.safe === null ? EXPIRED_REPLY : entry.safe,
  });
}

/**
 * expire drops every prompt past its TTL and answers what each one's expiry is
 * owed:
 *
 *     expire(queue, { now })
 *
 * It is a function of each prompt's own `at`, the queue's TTL and this `now`,
 * so **when the owner-run sweeper happens to run does not decide anything**: a
 * sweep can delete the stored prompt while the consumer is down, and the
 * consumer still expires it from its own copy on its own clock (P6.7, P6.13).
 * Each expired prompt yields its non-destructive answer, or a stop; delivering
 * it exactly once is the consumer's ledger's business, not this module's.
 *
 * @returns `{ ok: true, queue, expired: [...] }` or a refusal
 */
export const expire = guarded(expireUnguarded);

function expireUnguarded(queue, event = {}) {
  const opened = open(queue, event, TIME_ONLY_FIELDS, 'event');
  if (!opened.ok) return opened;
  const { queue: current, values } = opened;

  const expired = [];
  const kept = [];
  for (const entry of current.prompts) {
    if (expiredBy(entry.prompt.at, values.now, current.promptTtlMs)) {
      expired.push(expiryOf(entry));
    } else {
      kept.push(entry);
    }
  }
  // No sort, and none is needed: this walk is over `current.prompts`, which is
  // held in the queue's canonical order (`at`, then id), and that order IS the
  // listing order -- escalation is monotone in `at` (an older prompt has waited
  // at least as long, see `reminderStage`), so the most escalated question is
  // already the first one the walk meets. `index.test.js` asserts both halves:
  // the monotonicity, and that this list comes back oldest-first. An earlier
  // version sorted here with a comment claiming the listing order differed from
  // the storage order; it cannot differ, and a rule nothing can tell apart is a
  // rule no test defends.
  return Object.freeze({
    ok: true,
    queue: freezeQueue({ ...current, now: values.now, prompts: kept }),
    expired: Object.freeze(expired),
  });
}

/**
 * answer settles a prompt with the phone's answer:
 *
 *     answer(queue, { now, answer })
 *
 * `answer` is a validated P6.3 message naming the prompt in `reply_to`: of kind
 * `answer` for a prompt whose effect is not destructive, and of kind `action`
 * -- D169's action record, carrying the confirmation -- for one whose effect
 * is. §3.5's rule is applied here as a SHAPE rule, both ways round: a plain
 * `answer` to a destructive prompt is refused `not-a-confirmation` (the shape
 * the consumer must refuse), and a confirmation answers only a destructive
 * prompt. The MAC over the record is P6.7's to verify; what is decided here is
 * the shape and the WINDOW (D173):
 *
 *   non-destructive prompt   accepted while the PROMPT is alive -- the prompt's
 *                            TTL from the prompt's own `at`, so a consumer
 *                            offline longer than six hours still accepts it;
 *   destructive prompt       accepted only inside the COMMAND window from the
 *                            CONFIRMATION's own time -- §3.5 checks that window
 *                            from the confirmation time -- and a confirmation
 *                            must be fresh, which is P1.1's window for the kind
 *                            this module passes (`acceptanceKind`).
 *
 * A prompt that is already dead is refused `expired` with its expiry attached:
 * the caller must deliver the safe answer, not the answer the user gave late.
 * An answer older than the window its kind is judged by -- its own `at` for a
 * queued prompt, the confirmation's time for a confirmation -- is refused with
 * the code P1.1 uses for that window (`expired` for a queued prompt's answer,
 * `replay` for a confirmation) and carries no expiry, because nothing has died.
 *
 * @returns `{ ok: true, outcome: 'answered', queue, ... }` or a refusal
 */
export const answer = guarded(answerUnguarded);

function answerUnguarded(queue, event = {}) {
  const opened = open(queue, event, ANSWER_EVENT_FIELDS, 'event');
  if (!opened.ok) return opened;
  const { queue: current, values } = opened;

  const validated = validateMessage(values.answer);
  if (!validated.ok) {
    // The same line as `enqueue` draws: the schema defines which keys may be
    // echoed, and an unknown key is the writer's rather than this module's.
    return refuse(
      validated.code,
      MESSAGE_FIELDS.includes(validated.field) ? `answer.${validated.field}` : 'answer',
    );
  }
  const reply = validated.message;
  // D169's action record is the confirmation shape (§3.5), and it is what a
  // destructive prompt's answer IS -- not a second way to answer a quiet one.
  const isConfirmation = reply.kind === 'action';
  if (reply.kind !== 'answer' && !isConfirmation) {
    return refuse(REFUSAL.NOT_AN_ANSWER, 'answer.kind');
  }
  // The schema requires `reply_to` on an `answer` and merely permits it on an
  // `action` (`KIND_OPTIONAL`), so on the confirmation shape this is what makes
  // the record name a prompt at all -- and either way the lookup below would
  // read `undefined` without it. Restating P6.3's guarantee is not dead code:
  // `validateMessage` is another module's, and this one would rather refuse
  // than read a non-string into a lookup.
  if (typeof reply.reply_to !== 'string') return refuse(REFUSAL.UNKNOWN_PROMPT, 'answer.reply_to');

  const index = current.prompts.findIndex((entry) => entry.prompt.id === reply.reply_to);
  if (index === -1) return refuse(REFUSAL.UNKNOWN_PROMPT, 'answer.reply_to');
  const entry = current.prompts[index];

  // §3.5's shape rule, both ways round: an answer to a destructive prompt is
  // accepted only as an action record carrying a confirmation, never as a plain
  // `answer` (which the consumer must refuse), and a confirmation answers only
  // a prompt whose effect is destructive.
  if (entry.destructive !== isConfirmation) {
    return refuse(
      isConfirmation ? REFUSAL.NOT_AN_ANSWER : REFUSAL.NOT_A_CONFIRMATION,
      'answer.kind',
    );
  }

  // The envelope's own clock, held to the same bounds as any other time here.
  const envelope = checkStamp(reply.at, values.now, current.futureToleranceMs, 'answer.at');
  if (envelope !== null) return envelope;
  // And, for a confirmation, the time the command window is measured from:
  // §3.5's check is "6 hours from the confirmation time", not from the
  // envelope's `at`. P6.3's schema validated it as a timestamp, and this
  // module's own bounds -- `-0`, an unset counter, the ceiling, the tolerance
  // -- are applied here as they are to every other reading.
  let at = reply.at;
  if (isConfirmation) {
    at = reply.action.confirmation.at;
    const confirmed = checkStamp(
      at,
      values.now,
      current.futureToleranceMs,
      'answer.action.confirmation.at',
    );
    if (confirmed !== null) return confirmed;
  }

  if (expiredBy(entry.prompt.at, values.now, current.promptTtlMs)) {
    // The prompt died before the answer arrived. The user's late answer is NOT
    // handed over, and the expiry's is: D168's default is the non-destructive
    // option, and an answer to a question that expired is an answer to a
    // changed world.
    const refusal = {
      ok: false,
      code: REFUSAL.EXPIRED,
      field: 'prompt.at',
      expiry: expiryOf(entry),
    };
    return Object.freeze(refusal);
  }

  const kind = acceptanceKind(entry.destructive);
  const windowMs = windowFor(current, kind);
  if (values.now - at > windowMs) {
    // The answer's age -- the confirmation's for a confirmation, the message's
    // own `at` otherwise -- is past the window its kind is judged by, and the
    // code is the one P1.1 answers for that kind: `expired` for a delivery
    // handed to the prompt window, `replay` for a command. P6.7 passes this
    // very kind to P1.1's `deliver`, so a code chosen differently here would
    // have this module and the mailbox tell the caller two different stories
    // about one delivery. Two ways to reach it, one per kind:
    //
    //   a confirmation (a destructive prompt's answer) whose confirmation time
    //   is older than the command window -- the prompt itself may still be
    //   alive by days;
    //   an answer older than the TTL while its prompt is alive, which is only
    //   possible for an `at` written before the prompt was (a phone's clock is
    //   not this module's problem, but an answer to the prompt's own `at` or
    //   later is always inside the TTL that keeps the prompt alive).
    //
    // Neither carries an `expiry`: the prompt has not died, so there is nothing
    // owed. The dead-prompt refusal above returns the same `expired` code with
    // `field: 'prompt.at'` AND its expiry; the `field`, and that field's
    // presence, are what tell the two apart.
    return refuse(
      kind === KIND.PROMPT ? REFUSAL.EXPIRED : REFUSAL.REPLAY,
      isConfirmation ? 'answer.action.confirmation.at' : 'answer.at',
    );
  }

  const prompts = current.prompts.filter((_, position) => position !== index);
  return Object.freeze({
    ok: true,
    outcome: OUTCOME.ANSWERED,
    id: entry.prompt.id,
    kind,
    windowMs,
    destructive: entry.destructive,
    queue: freezeQueue({ ...current, now: values.now, prompts }),
  });
}

/** The window a kind is judged by: P1.1's own mapping, which is why it is here. */
function windowFor(queue, kind) {
  return kind === KIND.PROMPT ? queue.promptTtlMs : queue.windowMs;
}

/**
 * The kind an answer is delivered to P1.1's window API under (D173, §3.6):
 * `command` for an answer to a destructive prompt, because that answer is a
 * confirmation and a confirmation must be fresh; `prompt` otherwise, because a
 * prompt's lifetime is its TTL. Exported so P6.7's consumer passes exactly the
 * kind this module judged with -- the two ends cannot pick different windows.
 */
export const acceptanceKind = guarded(acceptanceKindUnguarded);

function acceptanceKindUnguarded(destructive) {
  return destructive ? KIND.COMMAND : KIND.PROMPT;
}

// ---------------------------------------------------------------------------
// Listing, announcing, and the count across installations
// ---------------------------------------------------------------------------

/**
 * A prompt as the app shows and speaks it: the message itself, its local
 * classification, when it was last announced, its reminder stage and whether it
 * is due to be read out now.
 */
function listing(entry, now) {
  return Object.freeze({
    prompt: entry.prompt,
    destructive: entry.destructive,
    safe: entry.safe,
    announcedAt: entry.announcedAt,
    stage: stageOf(entry.prompt.at, now),
    due: isDue(entry, now),
  });
}

/**
 * A prompt as `announce` reports it: the same fields as a listing entry, minus
 * `due` -- it has just been read out, so "is it due" has already been answered
 * by the fact that it is here, and a flag that says `false` beside the thing
 * being spoken would be worse than no flag at all. `stage` is the reminder the
 * pause is reading, so a late announcement reports the stage it was late for.
 */
function spokenItem(entry, now) {
  return Object.freeze({
    prompt: entry.prompt,
    destructive: entry.destructive,
    safe: entry.safe,
    announcedAt: entry.announcedAt,
    stage: stageOf(entry.prompt.at, now),
  });
}

/**
 * The listing order: the oldest first, then the id, which makes the order total
 * and independent of arrival -- the queue's own canonical order, applied to
 * either shape this module lists (an entry, which carries its `prompt`, or the
 * flat expiry record `expiryOf` returns).
 *
 * "The most escalated first" (§3.4's "listed first in what's pending") is this
 * same order and not a second rule: `reminderStage` is monotone in `at` (an
 * older prompt has waited at least as long), so a stage rule here would order
 * the list exactly as the `at` comparison already does. `index.test.js` asserts
 * the monotonicity that makes the two one rule, because a rule that cannot
 * differ from the order beneath it cannot be tested through this function.
 */
function compareForListing(left, right) {
  const leftAt = left.prompt ? left.prompt.at : left.at;
  const rightAt = right.prompt ? right.prompt.at : right.at;
  if (leftAt !== rightAt) return leftAt < rightAt ? -1 : 1;
  const leftId = left.prompt ? left.prompt.id : left.id;
  const rightId = right.prompt ? right.prompt.id : right.id;
  if (leftId !== rightId) return leftId < rightId ? -1 : 1;
  return 0;
}

/**
 * pending lists one installation's waiting prompts:
 *
 *     pending(queue, { now })
 *
 * In the listing order, each carrying its reminder stage and whether it is due
 * ("what's pending?", §3.4, and the text screen's list, P6.9). It changes
 * nothing: `announce` is what marks a prompt as spoken, and neither applies the
 * TTL -- the queue is one the caller has already expired (`expire`), which is
 * what keeps a dead prompt's answer from being dropped unspoken.
 *
 * @returns `{ ok: true, pending: [...] }` or a refusal
 */
export const pending = guarded(pendingUnguarded);

function pendingUnguarded(queue, event = {}) {
  const opened = open(queue, event, TIME_ONLY_FIELDS, 'event');
  if (!opened.ok) return opened;
  const { queue: current, values } = opened;
  const list = current.prompts
    .map((entry) => listing(entry, values.now))
    .sort(compareForListing);
  return Object.freeze({ ok: true, queue: current, pending: Object.freeze(list) });
}

/**
 * announce reads out what is due at a natural pause:
 *
 *     announce(queue, { now })
 *
 * This answers the entries whose question should be read at THIS pause --
 * never announced, or re-announced because an hour has passed, then a day
 * (§3.4) -- together with the queue they were read from. The pause's other
 * half, the COUNT of everything waiting across installations (the spec's "Two
 * questions waiting"), is `announcement`'s: it is the sentence that is spoken,
 * and this operation is about the active installation's own list rather than
 * about the count, so the count is not repeated in its answer. Marking the
 * spoken entries announced is the only mutation this module performs on a
 * queue, and it is the phone's state: whether the user has HEARD a question is
 * not something a database may know (D127) and not something a clock function
 * can decide.
 *
 * @returns `{ ok: true, queue, spoken: [...] }` or a refusal
 */
export const announce = guarded(announceUnguarded);

function announceUnguarded(queue, event = {}) {
  const opened = open(queue, event, TIME_ONLY_FIELDS, 'event');
  if (!opened.ok) return opened;
  const { queue: current, values } = opened;

  const due = current.prompts
    .filter((entry) => isDue(entry, values.now))
    .sort(compareForListing);

  // The entries are marked first and the `spoken` list is read from the marked
  // ones, so what the caller is handed is what the queue now holds: the
  // announcement time of a prompt just read out is the pause it was read at,
  // and a caller that persists the list cannot persist a value the queue has
  // already moved past.
  const marked = new Map();
  const prompts = current.prompts.map((entry) => {
    if (!isDue(entry, values.now)) return entry;
    const heard = Object.freeze({ ...entry, announcedAt: values.now });
    marked.set(entry.prompt.id, heard);
    return heard;
  });

  return Object.freeze({
    ok: true,
    queue: freezeQueue({ ...current, now: values.now, prompts }),
    spoken: Object.freeze(due.map((entry) => spokenItem(marked.get(entry.prompt.id), values.now))),
  });
}

/** The English words the count is spoken with, up to a dozen. */
const NUMBER_WORDS = Object.freeze([
  'Zero',
  'One',
  'Two',
  'Three',
  'Four',
  'Five',
  'Six',
  'Seven',
  'Eight',
  'Nine',
  'Ten',
  'Eleven',
  'Twelve',
]);

/**
 * The sentence a pause announces (§3.2's "Two questions waiting"), or `null`
 * when nothing is waiting -- an announcement of nothing is noise, and a caller
 * that speaks `null` says nothing.
 *
 * Beyond twelve the numeral is spoken rather than a word ("48 questions
 * waiting"): the count is bounded by the cap times the installations, so the
 * fallback exists for several installations at their cap, and that many
 * questions is a number a person wants to hear exactly.
 */
export const waitingSentence = guarded(waitingSentenceUnguarded);

function waitingSentenceUnguarded(count) {
  if (!Number.isSafeInteger(count) || count < 0) return null;
  if (count === 0) return null;
  const word = count < NUMBER_WORDS.length ? NUMBER_WORDS[count] : String(count);
  return `${word} question${count === 1 ? '' : 's'} waiting`;
}

/** readQueues reads the several installations' queues a cross-account view takes. */
function readQueues(queues, where) {
  if (!Array.isArray(queues)) return refuse(REFUSAL.INVALID_QUEUES, where);
  const read = [];
  const accounts = new Set();
  for (const value of queues) {
    const queue = readQueue(value, where);
    if (!queue.ok) return queue;
    if (accounts.has(queue.queue.account)) {
      // Two queues for one installation would count its questions twice and
      // list them twice. The account is the identity here, as it is everywhere
      // else in this plan (D114).
      return refuse(REFUSAL.INVALID_QUEUES, where);
    }
    accounts.add(queue.queue.account);
    read.push(queue.queue);
  }
  return { ok: true, queues: read };
}

/**
 * pendingAcross lists every installation's waiting prompts, grouped:
 *
 *     pendingAcross(queues, { now })
 *
 * "Per installation" is the spec's own words for "what's pending?" (§3.4), and
 * the grouping is not cosmetic: D170's overview shows one row per installation
 * with its own freshness and **no summed total**, and a list that merged two
 * installations' questions would be exactly that total. The installations keep
 * the order the caller gave them, which is the app's own.
 *
 * It applies **no TTL** to the queues it reads, like every other read here, so
 * it is a read over queues the caller has already expired -- see the header's
 * note on the caller's ordering. Applying the TTL per row would drop a dead
 * prompt from the very listing the overview is drawn from, hiding the question
 * whose safe answer or stop is owed exactly once.
 *
 * @returns `{ ok: true, count, accounts: [{ account, pending }] }` or a refusal
 */
export const pendingAcross = guarded(pendingAcrossUnguarded);

function pendingAcrossUnguarded(queues, event = {}) {
  const read = readQueues(queues, 'queues');
  if (!read.ok) return read;
  const time = readClock(event, 'event');
  if (!time.ok) return time;
  const now = time.values.now;

  const accounts = [];
  let count = 0;
  for (const queue of read.queues) {
    if (now < queue.now) return refuse(REFUSAL.NOW_WENT_BACKWARDS, 'now');
    const list = queue.prompts
      .map((entry) => listing(entry, now))
      .sort(compareForListing);
    count += list.length;
    accounts.push(Object.freeze({ account: queue.account, pending: Object.freeze(list) }));
  }
  return Object.freeze({
    ok: true,
    count,
    accounts: Object.freeze(accounts),
  });
}

/**
 * announcement counts every account's waiting prompts and says which questions
 * are due to be read at this pause:
 *
 *     announcement(queues, { now })  ->  { count, text, due: [...] }
 *
 * The COUNT is across accounts -- the spec's example is one sentence for the
 * phone, not one per installation -- while the `due` list keeps each prompt
 * with its account, so the app can say which installation is asking before it
 * reads the question out.
 */
export const announcement = guarded(announcementUnguarded);

function announcementUnguarded(queues, event = {}) {
  const read = readQueues(queues, 'queues');
  if (!read.ok) return read;
  const time = readClock(event, 'event');
  if (!time.ok) return time;
  const now = time.values.now;

  let count = 0;
  const due = [];
  for (const queue of read.queues) {
    if (now < queue.now) return refuse(REFUSAL.NOW_WENT_BACKWARDS, 'now');
    count += queue.prompts.length;
    const mine = queue.prompts
      .filter((entry) => isDue(entry, now))
      .sort(compareForListing);
    for (const entry of mine) {
      due.push(Object.freeze({ account: queue.account, ...listing(entry, now) }));
    }
  }
  return Object.freeze({
    ok: true,
    count,
    text: waitingSentence(count),
    due: Object.freeze(due),
  });
}

// ---------------------------------------------------------------------------
// The interrupt policy (D160, D127)
// ---------------------------------------------------------------------------

/** The policy's own keys, exactly. */
const POLICY_FIELDS = Object.freeze(['queue', 'digest', 'presence', 'caps', 'switch', 'now']);
const PRESENCE_FIELDS = Object.freeze(['state', 'previous']);
const CAP_FIELDS = Object.freeze(['reached', 'near']);

/**
 * The interrupt policy: whether anything may be spoken over the user, and why.
 *
 *     evaluateInterrupt({ queue, digest, presence, caps, switch, now })
 *
 *   digest    P1.3's result, or `null` when no document has been read. Only its
 *             `severity` is read: `kill` is the ladder's top rung and is D160's
 *             first event. A severity off the ladder is a refusal, never a
 *             default -- a value nobody defined must not rank as `info`.
 *   presence  `{ state, previous }`: P1.4's state for this installation now,
 *             and the state the app last showed. D160's offline event is a
 *             TRANSITION ("going from live to offline"), so a first sighting of
 *             an offline consumer -- `previous` absent -- is not an
 *             interruption: there is no change to announce. That is fail-quiet
 *             on purpose, and it is recorded here rather than left to be found.
 *   caps      `{ reached, near }`, RECOMPUTED ON THE PHONE from `spend/$day/**`
 *             and `config/caps/**`: the phone cannot see per-run spend or
 *             halted runs (tension (b1)), so nothing else can answer D160's
 *             second and third events. `near` is the local threshold the owner
 *             left available and off by default, so the caller passes false
 *             until the user turns it on.
 *   switch    D127's local setting: it narrows D160's set or widens it to "any
 *             prompt" (see `SWITCH`); absent means the default.
 *
 * The three signals are optional too, and each of them reads `null` exactly as
 * it reads absence -- the app has nothing to report for that signal. That is
 * not the same fault as a signal it cannot read: a value that is neither is
 * refused, never taken for "no event" (`invalid-severity`, `invalid-presence`,
 * `invalid-caps`), because a kill switch the app cannot read must not be
 * answered as a kill switch that is not set.
 *
 * The order of the answers is the severity order -- kill, then the cap, then
 * the consumer, then a prompt -- so two events at once report the one that
 * matters most, and there is no cell of the (event x switch) table left
 * undecided: `index.test.js` asserts the whole table rather than the examples.
 *
 * It applies **no TTL** to the queue it reads (`widen`'s `due` is the reminder
 * schedule, not the prompt's life), so it is a read over a queue the caller has
 * already expired -- see the header's note on the caller's ordering.
 *
 * @returns `{ ok: true, interrupt, reason, pending }` or a refusal
 */
export const evaluateInterrupt = guarded(evaluateInterruptUnguarded);

function evaluateInterruptUnguarded(input = {}) {
  const read = readFields(input, POLICY_FIELDS, 'input');
  if (!read.ok) return read;
  const values = read.values;

  const current = readQueue(values.queue, 'queue');
  if (!current.ok) return current;
  const queue = current.queue;

  if (values.now === undefined) return refuse(REFUSAL.MISSING_FIELD, 'now');
  if (!isIntegerInRange(values.now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');
  if (values.now < queue.now) return refuse(REFUSAL.NOW_WENT_BACKWARDS, 'now');

  const kill = readKill(values.digest);
  if (kill.bad) return kill.bad;

  const presence = readPresence(values.presence);
  if (presence.bad) return presence.bad;

  const caps = readCaps(values.caps);
  if (caps.bad) return caps.bad;

  const setting = values.switch === undefined ? SWITCH.DEFAULT : values.switch;
  if (!SWITCH_VALUES.includes(setting)) return refuse(REFUSAL.INVALID_SWITCH, 'switch');

  const pendingCount = queue.prompts.length;
  const reason = decide({
    kill: kill.value,
    reached: caps.value.reached,
    near: caps.value.near,
    offline: presence.value.offline,
    due: queue.prompts.some((entry) => isDue(entry, values.now)),
    setting,
  });

  return Object.freeze({
    ok: true,
    interrupt: reason !== null,
    reason,
    pending: pendingCount,
  });
}

/**
 * The (event x switch) table, as code. D160 fixes the set; the switch narrows
 * it to the two events that mean the work or the money has stopped, or widens
 * it to "any prompt". Locked out by the narrowing: the offline transition and
 * the local cap-near threshold.
 */
function decide({ kill, reached, near, offline, due, setting }) {
  if (kill) return INTERRUPT.KILL;
  if (reached) return INTERRUPT.CAP_REACHED;
  if (setting === SWITCH.NARROW) return null;
  if (near) return INTERRUPT.CAP_NEAR;
  if (offline) return INTERRUPT.OFFLINE;
  if (setting === SWITCH.WIDEN && due) return INTERRUPT.PROMPT;
  return null;
}

/**
 * The kill-switch signal: P1.3's ladder's top rung, or nothing.
 *
 * `severity` is the one field of another module's result this policy reads, and
 * it is read exactly as this module reads its own fields: once, through its own
 * descriptor. A plain `digest.severity` would CALL a getter and believe what it
 * answered -- and a getter can answer differently each time, which is the one
 * thing an answer about whether the app may speak over the user cannot be built
 * on. The other keys are P1.3's business (`project`, `total`, `items`), so no
 * vocabulary is imposed on them here: this is `readFields`' discipline without
 * `readFields`' key list, for a value this module does not own.
 */
function readKill(digest) {
  if (digest === undefined || digest === null) return { value: false };
  if (!isPlainObject(digest)) return { bad: refuse(REFUSAL.INVALID_SEVERITY, 'digest') };
  const descriptor = Object.getOwnPropertyDescriptor(digest, 'severity');
  if (descriptor !== undefined && !Object.hasOwn(descriptor, 'value')) {
    return { bad: refuse(REFUSAL.ACCESSOR_PROPERTY, 'digest.severity') };
  }
  const severity = descriptor === undefined ? undefined : descriptor.value;
  if (severity === undefined) return { bad: refuse(REFUSAL.INVALID_SEVERITY, 'digest.severity') };
  try {
    severityRank(severity);
  } catch {
    return { bad: refuse(REFUSAL.INVALID_SEVERITY, 'digest.severity') };
  }
  return { value: severity === SEVERITY.kill };
}

/** The presence signal: P1.4's state names, and the transition D160 names. */
function readPresence(presence) {
  if (presence === undefined || presence === null) return { value: { offline: false } };
  const read = readFields(presence, PRESENCE_FIELDS, 'presence');
  if (!read.ok) return { bad: refuse(REFUSAL.INVALID_PRESENCE, 'presence') };
  const values = read.values;
  if (!PRESENCE_STATES.includes(values.state)) {
    return { bad: refuse(REFUSAL.INVALID_PRESENCE, 'presence.state') };
  }
  const previous = values.previous;
  if (previous !== undefined && previous !== null && !PRESENCE_STATES.includes(previous)) {
    return { bad: refuse(REFUSAL.INVALID_PRESENCE, 'presence.previous') };
  }
  const offline =
    values.state === PRESENCE.OFFLINE &&
    previous !== undefined &&
    previous !== null &&
    previous !== PRESENCE.OFFLINE;
  return { value: { offline } };
}

/** The cap signals: recomputed on the phone, validated here as booleans. */
function readCaps(caps) {
  if (caps === undefined || caps === null) return { value: { reached: false, near: false } };
  const read = readFields(caps, CAP_FIELDS, 'caps');
  if (!read.ok) return { bad: refuse(REFUSAL.INVALID_CAPS, 'caps') };
  const values = read.values;
  for (const field of CAP_FIELDS) {
    if (values[field] !== undefined && typeof values[field] !== 'boolean') {
      return { bad: refuse(REFUSAL.INVALID_CAPS, `caps.${field}`) };
    }
  }
  return { value: { reached: values.reached === true, near: values.near === true } };
}
