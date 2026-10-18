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
// hours** (O5, 2026-10-17, inside P1.1's seven-day cap). So
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
// action record, not free text.
//
// This module does not re-implement the windows: it **passes the kind** to
// P1.1's kind-parameterised API (`KIND.COMMAND` or `KIND.PROMPT`, the same
// vocabulary P1.1's `deliver` reads) and `acceptanceKind` is the mapping. What
// is checked here is the other half, which P1.1 cannot check: whether the
// PROMPT is still alive, and whether a destructive answer's own `at` is inside
// the command window.
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
// schema. `shared/prompts/README.md` is the module's own contract, and
// `../../docs/mobile/implementation-plan.md`'s P6.5 is this module's task.

import { severityRank, SEVERITY } from '../digest/severity.js';
import {
  DEFAULT_FUTURE_TOLERANCE_MS,
  DEFAULT_WINDOW_MS,
  KIND,
  MAX_PROMPT_TTL_MS,
  MAX_WINDOW_MS,
  MIN_WINDOW_MS,
  TIME_MAX,
} from '../mailbox/index.js';
import { OPTION_MAX, validateMessage } from '../mailbox-schema/index.js';
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

  INVALID_NOW: 'invalid-now',
  NOW_WENT_BACKWARDS: 'now-went-backwards',
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
 * readFields reads exactly the named fields into a null-prototype snapshot,
 * each field once, through its own descriptor.
 *
 * A value that is not a plain data object is refused (`not-an-object`), a key
 * the caller does not name -- a symbol included -- is refused (`unknown-field`),
 * and an accessor property is refused (`accessor-property`) without ever being
 * called: a getter can answer differently each time it is read, and this
 * module's whole claim is that its answer is a function of the value it was
 * handed. This is P1.1's reader, at P6.5's size.
 */
function readFields(value, names, where) {
  if (!isPlainObject(value)) return refuse(REFUSAL.INPUT_NOT_AN_OBJECT, where);

  const values = Object.create(null);
  for (const key of Reflect.ownKeys(value)) {
    if (!names.includes(key)) return refuse(REFUSAL.UNKNOWN_FIELD, where);
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (descriptor === undefined || !Object.hasOwn(descriptor, 'value')) {
      return refuse(REFUSAL.UNKNOWN_FIELD, key);
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
 */
export function isExpired(at, now, promptTtlMs) {
  return now - at > promptTtlMs;
}

/**
 * How many times a prompt waiting since `at` has come due to be re-announced by
 * `now`: 0 before the first hour, 1 from one hour on, 2 from one hour and a day
 * on, and so on. A pure function of the clock -- the schedule is anchored to
 * the prompt's own arrival -- while whether it has actually been SPOKEN is the
 * entry's local `announcedAt`, because this module cannot know when the app's
 * natural pauses were.
 */
export function reminderStage(at, now) {
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
// A prompt announced before its first reminder is not due again until the hour
// has passed, and the arithmetic says so on its own: at stage 0 the reminder
// time is `at - 23h`, which no announcement can precede -- an announcement is
// made at the queue's own `now`, and a prompt's `at` is never more than the
// future tolerance above it. So there is no stage-0 branch here to be
// redundant; a guard that cannot be told apart from the fall-through is a
// guard no test can defend.
function isDue(entry, now) {
  if (entry.announcedAt === null) return true;
  const stage = reminderStage(entry.prompt.at, now);
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
 * It returns a frozen `{ ok: true, queue }` or a refusal; it never throws.
 *
 * @param {{ now: number, account: string, promptTtlMs?: number, windowMs?: number,
 *           maxPending?: number, futureToleranceMs?: number }} [options]
 * @returns {{ ok: true, queue: object } | { ok: false, code: string, field: string | null }}
 */
export function initialQueue(options = {}) {
  const read = readFields(
    options,
    QUEUE_FIELDS.filter((field) => field !== 'prompts'),
    'options',
  );
  if (!read.ok) return read;
  const values = read.values;

  const now = values.now;
  if (now === undefined) return refuse(REFUSAL.UNKNOWN_FIELD, 'now');
  if (!isIntegerInRange(now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');

  const account = values.account;
  if (account === undefined) return refuse(REFUSAL.UNKNOWN_FIELD, 'account');
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
  if (!isIntegerInRange(futureToleranceMs, 0, MAX_WINDOW_MS)) {
    return refuse(REFUSAL.INVALID_FUTURE_TOLERANCE, 'futureToleranceMs');
  }

  return {
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
  };
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
  if (!isIntegerInRange(values.futureToleranceMs, 0, MAX_WINDOW_MS)) {
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
 * reminder that was already spoken. A forward jump is deliberately **not**
 * bounded, unlike P1.1's -- there is no ledger here whose ordering a jump could
 * corrupt; a jump merely expires prompts, which is what the clock says.
 */
function open(queue, event, names, where) {
  const current = readQueue(queue, 'queue');
  if (!current.ok) return current;
  const read = readFields(event, names, where);
  if (!read.ok) return read;
  if (read.values.now === undefined) return refuse(REFUSAL.UNKNOWN_FIELD, 'now');
  if (!isIntegerInRange(read.values.now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');
  if (read.values.now < current.queue.now) return refuse(REFUSAL.NOW_WENT_BACKWARDS, 'now');
  return { ok: true, queue: current.queue, values: read.values };
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
 * timestamp bounds, and every code P6.3's validator raises, prefixed `prompt.`.
 *
 * @param {object} queue a queue, as returned by initialQueue or an operation
 * @param {{ now: number, prompt: object, destructive?: boolean, safe?: string }} event
 * @returns {object} `{ ok: true, outcome, queue, ... }` or a refusal
 */
export function enqueue(queue, event) {
  const opened = open(queue, event, DELIVERY_FIELDS, 'event');
  if (!opened.ok) return opened;
  const { queue: current, values } = opened;

  const validated = validateMessage(values.prompt);
  if (!validated.ok) {
    // The schema names an unknown field by its key, which the writer chose:
    // only a field the schema itself defines is echoed, anything else is
    // reported as the prompt as a whole. P1.1's rule, for the same reason.
    return refuse(validated.code, validated.field === null ? 'prompt' : `prompt.${validated.field}`);
  }
  const prompt = validated.message;
  if (prompt.kind !== KIND_OF_PROMPT) return refuse(REFUSAL.NOT_A_PROMPT, 'prompt.kind');

  if (Object.is(prompt.at, -0)) return refuse(REFUSAL.NEGATIVE_ZERO, 'prompt.at');
  // `at: 0` is schema-valid and is not a time this module will carry: the TTL
  // would be measured from an unset counter, so the prompt would be "expired"
  // from the moment it was written. P1.1 refuses the same value for the same
  // reason.
  if (prompt.at < 1) return refuse(REFUSAL.ZERO_TIMESTAMP, 'prompt.at');
  if (prompt.at - values.now > current.futureToleranceMs) {
    return refuse(REFUSAL.FUTURE_TIMESTAMP, 'prompt.at');
  }

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

  if (isExpired(prompt.at, values.now, current.promptTtlMs)) {
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
export function expire(queue, event = {}) {
  const opened = open(queue, event, TIME_ONLY_FIELDS, 'event');
  if (!opened.ok) return opened;
  const { queue: current, values } = opened;

  const expired = [];
  const kept = [];
  for (const entry of current.prompts) {
    if (isExpired(entry.prompt.at, values.now, current.promptTtlMs)) {
      expired.push(expiryOf(entry));
    } else {
      kept.push(entry);
    }
  }
  // The listing order, not the storage order: an expired question that had
  // escalated is the one the orchestrator has been waiting on the longest.
  expired.sort((left, right) => compareForListing(left, values.now, right, values.now));

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
 * `answer` is a validated P6.3 message of kind `answer` naming the prompt in
 * `reply_to` -- and, for a destructive prompt, it is a confirmation's shape
 * (D169's action record is P6.7's to re-validate; what is decided here is the
 * WINDOW, D173):
 *
 *   non-destructive prompt   accepted while the PROMPT is alive -- the prompt's
 *                            TTL from the prompt's own `at`, so a consumer
 *                            offline longer than six hours still accepts it;
 *   destructive prompt       accepted only inside the COMMAND window from the
 *                            ANSWER's own `at` -- a confirmation must be fresh,
 *                            which is P1.1's window for the kind this module
 *                            passes (`acceptanceKind`).
 *
 * A prompt that is already dead is refused `expired` with its expiry attached:
 * the caller must deliver the safe answer, not the answer the user gave late.
 *
 * @returns `{ ok: true, outcome: 'answered', queue, ... }` or a refusal
 */
export function answer(queue, event = {}) {
  const opened = open(queue, event, ANSWER_EVENT_FIELDS, 'event');
  if (!opened.ok) return opened;
  const { queue: current, values } = opened;

  const validated = validateMessage(values.answer);
  if (!validated.ok) {
    return refuse(
      validated.code,
      validated.field === null ? 'answer' : `answer.${validated.field}`,
    );
  }
  const reply = validated.message;
  if (reply.kind !== 'answer') return refuse(REFUSAL.NOT_AN_ANSWER, 'answer.kind');
  // `reply_to` is required by the schema on an `answer`, so it is present; the
  // check here is the schema's guarantee restated as this module's assumption,
  // and it is not dead code: `validateMessage` is P6.3's, and this module would
  // rather refuse than read `undefined` into a lookup.
  if (typeof reply.reply_to !== 'string') return refuse(REFUSAL.UNKNOWN_PROMPT, 'answer.reply_to');

  const index = current.prompts.findIndex((entry) => entry.prompt.id === reply.reply_to);
  if (index === -1) return refuse(REFUSAL.UNKNOWN_PROMPT, 'answer.reply_to');
  const entry = current.prompts[index];

  if (Object.is(reply.at, -0)) return refuse(REFUSAL.NEGATIVE_ZERO, 'answer.at');
  if (reply.at < 1) return refuse(REFUSAL.ZERO_TIMESTAMP, 'answer.at');
  if (reply.at - values.now > current.futureToleranceMs) {
    return refuse(REFUSAL.FUTURE_TIMESTAMP, 'answer.at');
  }

  if (isExpired(entry.prompt.at, values.now, current.promptTtlMs)) {
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
  if (values.now - reply.at > windowMs) {
    // Only a destructive prompt can reach this: a non-destructive answer is
    // judged by the prompt's life above, and its own age is irrelevant while
    // the prompt lives.
    return refuse(REFUSAL.REPLAY, 'answer.at');
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
export function acceptanceKind(destructive) {
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
    stage: reminderStage(entry.prompt.at, now),
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
    stage: reminderStage(entry.prompt.at, now),
  });
}

/**
 * The listing order: the most escalated first -- a re-announced prompt is
 * "listed first in what's pending" (§3.4) -- then the oldest, then the id,
 * which makes the order total and independent of arrival.
 */
function compareForListing(left, leftNow, right, rightNow) {
  const leftStage = reminderStage(left.prompt ? left.prompt.at : left.at, leftNow);
  const rightStage = reminderStage(right.prompt ? right.prompt.at : right.at, rightNow);
  if (leftStage !== rightStage) return rightStage - leftStage;
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
 * nothing: `announce` is what marks a prompt as spoken.
 *
 * @returns `{ ok: true, pending: [...] }` or a refusal
 */
export function pending(queue, event = {}) {
  const opened = open(queue, event, TIME_ONLY_FIELDS, 'event');
  if (!opened.ok) return opened;
  const { queue: current, values } = opened;
  const list = current.prompts
    .map((entry) => listing(entry, values.now))
    .sort((left, right) => compareForListing(left, values.now, right, values.now));
  return Object.freeze({ ok: true, queue: current, pending: Object.freeze(list) });
}

/**
 * announce reads out what is due at a natural pause:
 *
 *     announce(queue, { now })
 *
 * Two things are answered together, because the pause is one event: the COUNT
 * of everything waiting (the spec's "Two questions waiting") and the entries
 * whose question should actually be read this time -- never announced, or
 * re-announced because an hour has passed, then a day (§3.4). Marking them
 * announced is the only mutation this module performs on a queue, and it is the
 * phone's state: whether the user has HEARD a question is not something a
 * database may know (D127) and not something a clock function can decide.
 *
 * @returns `{ ok: true, queue, spoken: [...] }` or a refusal
 */
export function announce(queue, event = {}) {
  const opened = open(queue, event, TIME_ONLY_FIELDS, 'event');
  if (!opened.ok) return opened;
  const { queue: current, values } = opened;

  const due = current.prompts
    .filter((entry) => isDue(entry, values.now))
    .sort((left, right) => compareForListing(left, values.now, right, values.now));

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
export function waitingSentence(count) {
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
 * @returns `{ ok: true, count, accounts: [{ account, pending }] }` or a refusal
 */
export function pendingAcross(queues, event = {}) {
  const read = readQueues(queues, 'queues');
  if (!read.ok) return read;
  const time = readFields(event, TIME_ONLY_FIELDS, 'event');
  if (!time.ok) return time;
  const now = time.values.now;
  if (!isIntegerInRange(now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');

  const accounts = [];
  let count = 0;
  for (const queue of read.queues) {
    if (now < queue.now) return refuse(REFUSAL.NOW_WENT_BACKWARDS, 'now');
    const list = queue.prompts
      .map((entry) => listing(entry, now))
      .sort((left, right) => compareForListing(left, now, right, now));
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
export function announcement(queues, event = {}) {
  const read = readQueues(queues, 'queues');
  if (!read.ok) return read;
  const time = readFields(event, TIME_ONLY_FIELDS, 'event');
  if (!time.ok) return time;
  const now = time.values.now;
  if (!isIntegerInRange(now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');

  let count = 0;
  const due = [];
  for (const queue of read.queues) {
    if (now < queue.now) return refuse(REFUSAL.NOW_WENT_BACKWARDS, 'now');
    count += queue.prompts.length;
    const mine = queue.prompts
      .filter((entry) => isDue(entry, now))
      .sort((left, right) => compareForListing(left, now, right, now));
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
 *             prompt" (see `SWITCH`).
 *
 * The order of the answers is the severity order -- kill, then the cap, then
 * the consumer, then a prompt -- so two events at once report the one that
 * matters most, and there is no cell of the (event x switch) table left
 * undecided: `index.test.js` asserts the whole table rather than the examples.
 *
 * @returns `{ ok: true, interrupt, reason, pending }` or a refusal
 */
export function evaluateInterrupt(input = {}) {
  const read = readFields(input, POLICY_FIELDS, 'input');
  if (!read.ok) return read;
  const values = read.values;

  const current = readQueue(values.queue, 'queue');
  if (!current.ok) return current;
  const queue = current.queue;

  if (values.now === undefined) return refuse(REFUSAL.UNKNOWN_FIELD, 'now');
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

/** The kill-switch signal: P1.3's ladder's top rung, or nothing. */
function readKill(digest) {
  if (digest === undefined || digest === null) return { value: false };
  if (!isPlainObject(digest)) return { bad: refuse(REFUSAL.INVALID_SEVERITY, 'digest') };
  const severity = digest.severity;
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
  if (caps === undefined) return { value: { reached: false, near: false } };
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
