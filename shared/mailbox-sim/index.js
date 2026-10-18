// The deterministic mailbox simulator (mobile plan, Task P1.5).
//
// **What it is.** A harness, not a rule. It puts the three merged pure modules
// together — the message schema (`../mailbox-schema/`), the mailbox state
// machine (`../mailbox/`) and the consumer lease (`../lease/`) — behind an
// in-memory model of the store and a wire that can duplicate, reorder, drop
// and delay a delivery, so that the end-to-end mailbox test runs with **no
// runtime, no network and no clock**, and so that the properties the modules
// promise can be checked over many runs rather than asserted in prose. It is
// what makes check 80 a formality rather than a discovery.
//
// **It re-implements none of their rules.** A message is the schema's to
// accept; a delivery, a dedupe and a window are the state machine's to judge;
// who may act is the lease's to say. Where this file appears to decide
// something, it is deciding *its own* behaviour (when an actor retries, what a
// reply's id is), and the README says which of those are the harness's own
// inventions. Two rules the plan needs and the modules do not expose are
// **not** copied here; they are named in the README and in the pull request:
//
//   * the message's `kind` is not a schema field yet (P6.3 adds it, and P6.3
//     is proposed and NOT APPROVED), so the kind travels **beside** the
//     message, in the store's own record, and never inside it;
//   * nothing links a reply to the message it answers (P6.3's `reply_to` is
//     the same proposal), so the harness uses a naming convention —
//     `replyId(id) === 'r-' + id` — and says so.
//
// **Determinism, and the four injected things.** Nothing here reads a clock,
// a random source, a file or the network. The time is an injected clock
// (`manualClock` by default), the randomness is a seeded PRNG written in this
// file, the fault schedule is **data** (a seed, an op vocabulary, a per-actor
// clock skew and an explicit per-frame fault list), and the store is a plain
// map. A test patches `Date.now` and `Math.random` and runs a whole
// simulation to prove the first two.
//
// **Replayable from its seed.** `faultFor(schedule, frame)` is a pure function
// of the schedule and the frame's number — no accumulated state, so the same
// seed decides the same fault for the same frame whenever it is asked. A run
// therefore records every fault it applied, and `sim.schedule()` hands back
// the **expanded** schedule: the same seed with every drawn fault written out
// by frame number. A failing run is reproducible from its seed *and* from that
// text, and both are what a failure message prints.
//
// **The store, and the compare-and-swap.** The store holds the account's
// inbox, its outbox, the consumer's heartbeat, the consumer lease and the
// mailbox's ledger. The three message families are plain maps written by
// whoever writes them. The lease and the ledger are **one persisted value**,
// replaced only by compare-and-swap: a consumer reads a generation, decides
// against that snapshot, and commits only if the generation has not moved.
// That is the caller's duty the lease's README names ("the state is replaced
// only by compare-and-swap... two consumers that both read the same state and
// both get `ok: true` from `takeLease` must not both be able to write"), and it
// is the fence that makes the at-most-once property true when two consumers
// race — not the ledger alone.
//
// **What the actors do**, and which of it is the harness's own choice:
//
//   * the **phone** writes its own inbox messages, re-sends one until the
//     outbox answers it, and reads (speaks) every reply it can see. It writes
//     no other path.
//   * a **consumer** takes the lease, and if it does not hold it, writes
//     nothing and acts on nothing. If it holds it, it writes the heartbeat,
//     delivers every unacked inbox message to the ledger, acts on every
//     un-acted delivery, oldest first, and then writes what it owes: a reply for every record
//     it **performed** on, and the ack for every record the ledger holds as
//     `acted`. The two are not the same set — a stale record is `acted` and was
//     not performed — and the ledger can only speak for the second, which is
//     why the reply obligation lives with the consumer that performed (see
//     `actCommit`). The ledger's transition is committed **before** the action
//     is performed, so a lost race performs nothing.
//   * a **retry re-sends the same message**, `at` included: the caller's duty
//     the mailbox's README states ("the phone must never retry an id with a
//     new `at`"), and the reason a duplicated frame is harmless.
//
// The harness's own inventions, stated rather than hidden: the retry-until-
// observed duty, the `r-` reply-id convention, the out-of-band `kind`, and the
// ack being **one** write by the consumer (the plan's P1.5 says "a consumer
// leases, acts, acks and replies"; D113's *phone-written* ack is P3.6's, and
// the simulator does not model two ack writers).
//
// `shared/README.md` is the package's contract, and
// `../../docs/mobile/implementation-plan.md`'s P1.5 is this file's task.

import { LEASE_CODES, MAX_CLOCK_SKEW, emptyLeaseState, readLease, takeLease } from '../lease/index.js';
import { validateMessage } from '../mailbox-schema/index.js';
import {
  EVENT,
  KIND,
  OUTCOME,
  STATUS,
  initialState,
  step,
} from '../mailbox/index.js';

/**
 * The seed a caller gets when it names none. It is a date, not a measurement:
 * a schedule is a seed and nothing else, so the default only has to be a
 * number a reader can recognise in a failure message.
 */
export const DEFAULT_SEED = 20_261_017;

/**
 * The instant a simulated run starts at, in epoch milliseconds: 2026-01-01Z.
 * A constant rather than a clock read — `Date.UTC` is arithmetic, and this
 * module never asks the machine what time it is.
 */
export const DEFAULT_START_MS = Date.UTC(2026, 0, 1);

/** The chance a frame is faulted in a generated schedule. */
export const DEFAULT_RATE = 0.25;

/** The longest a `delay` fault may hold a frame, in milliseconds. */
export const DEFAULT_MAX_DELAY_MS = 30_000;
export const MAX_FRAME_DELAY_MS = 24 * 60 * 60 * 1_000;

/**
 * The largest clock skew a schedule may name, in milliseconds: one day. The
 * bound exists so that a schedule cannot hold a message for ever; it is **not**
 * the tolerance the modules judge by. `scheduleFromSeed` only ever draws a
 * skew within the lease's `MAX_CLOCK_SKEW` — the fault the model names — and a
 * hand-written schedule is allowed past it on purpose, because that is how the
 * boundary is tested: one millisecond beyond the tolerance the mailbox refuses
 * the delivery (`future-timestamp`) and the lease may be taken over.
 */
export const MAX_SKEW_MS = 24 * 60 * 60 * 1_000;

/**
 * The wire's faults. Four of them, each a property of one frame:
 *
 *   duplicate  the frame is delivered twice
 *   reorder    the frame arrives after the frame beside it
 *   drop       the frame never arrives
 *   delay      the frame arrives later, its `at` unchanged
 *
 * Clock skew is **not** here: it is a property of an actor's clock rather than
 * of one frame (`schedule.clocks`), because that is what it is — the same
 * writer stamps every message it writes with the same disagreement, and the
 * lease's skew rule compares two holders' clocks.
 */
export const FAULT = Object.freeze({
  DUPLICATE: 'duplicate',
  REORDER: 'reorder',
  DROP: 'drop',
  DELAY: 'delay',
});

/** The wire's fault vocabulary, for a schedule's `ops`. */
export const WIRE_OPS = Object.freeze(Object.values(FAULT));

/** The ids this module gives the actors it builds. */
export const PHONE = 'phone';

/** How many passes `run` may take before it refuses to go on. */
export const MAX_TICKS = 1_000;

/**
 * The tag every message the simulation writes carries. P6.3 requires a `mac`
 * on every message of every kind, and the schema validates its shape; the
 * simulation says nothing about its VALUE, because the tag is the consumer's
 * to check with the per-pairing key it holds and the harness does not (D171),
 * and a simulated key would be a key-shaped literal in a repository. So the
 * phone writes a mac-SHAPED placeholder — 64 lowercase hex characters no key
 * produces — and the end-to-end tests that do care about a real tag over real
 * bytes use `shared/mailbox-schema`'s vectors.
 */
export const PLACEHOLDER_MAC = 'f'.repeat(64);

/** The refusals, each with a code of its own. */
export const REFUSAL = Object.freeze({
  NOT_AN_OBJECT: 'not-an-object',
  UNKNOWN_FIELD: 'unknown-field',
  MISSING_FIELD: 'missing-field',
  BAD_TYPE: 'bad-type',
  BAD_SEED: 'bad-seed',
  BAD_RATE: 'bad-rate',
  BAD_OPS: 'bad-ops',
  UNKNOWN_OP: 'unknown-op',
  BAD_CLOCKS: 'bad-clocks',
  BAD_SKEW: 'bad-skew',
  BAD_DELAY: 'bad-delay',
  BAD_FAULT: 'bad-fault',
  MIXED_SCHEDULE: 'mixed-schedule',
  BAD_SCHEDULE_TEXT: 'bad-schedule-text',
  BAD_MILLISECONDS: 'bad-milliseconds',
  CAS_CONFLICT: 'cas-conflict',
  UNKNOWN_ACTOR: 'unknown-actor',
  DUPLICATE_ACTOR: 'duplicate-actor',
  NOT_HOLDING: 'not-holding',
  NO_CLOCK: 'no-clock',
  TOO_MANY_TICKS: 'too-many-ticks',
});

// ---------------------------------------------------------------------------
// The seeded PRNG, written here so that nothing is random
// ---------------------------------------------------------------------------

/**
 * A 32-bit mixer, used both to stir a seed and to derive one frame's
 * randomness from the schedule's seed and the frame's number. Every step is
 * 32-bit integer arithmetic (`Math.imul`, `>>>`), which is exact in a double
 * and identical in every engine — a PRNG whose output depended on floating
 * point rounding would not be replayable on another machine.
 */
function mix32(a, b) {
  let h = (a ^ Math.imul(b ^ 0x9e3779b9, 0x85ebca6b)) >>> 0;
  h = Math.imul(h ^ (h >>> 13), 0xc2b2ae35) >>> 0;
  return (h ^ (h >>> 16)) >>> 0;
}

function rngOf(seed) {
  // mulberry32: one 32-bit word of state, stepped by a golden-ratio odd
  // constant, then avalanched. It is not a cryptographic generator and does
  // not pretend to be one — the only thing asked of it is that the same seed
  // gives the same sequence everywhere.
  let state = mix32(seed, 0x1234_5678);
  const next = () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4_294_967_296;
  };
  const nextInt = (bound) => Math.floor(next() * bound);
  return {
    next,
    nextInt,
    pick: (list) => list[nextInt(list.length)],
    chance: (probability) => next() < probability,
  };
}

/**
 * makeRng builds the seeded generator this module draws with.
 *
 *     makeRng(seed) -> { ok: true, next, nextInt, pick, chance }
 *                    | { ok: false, code: 'bad-seed', field: 'seed' }
 *
 * A seed is a whole number in `0 .. 2^32 - 1`; anything else is refused rather
 * than coerced, because a coerced seed is a schedule nobody can replay.
 * `next()` answers a number in `[0, 1)`, `nextInt(bound)` a whole one in
 * `[0, bound)`, `pick(list)` an element, `chance(p)` a boolean.
 *
 * @param {unknown} seed
 * @returns {{ ok: true, next: () => number, nextInt: (bound: number) => number,
 *             pick: (list: Array<unknown>) => unknown, chance: (p: number) => boolean }
 *          | { ok: false, code: string, field: string }}
 */
export function makeRng(seed) {
  if (!isSeed(seed)) return refuse(REFUSAL.BAD_SEED, 'seed');
  return Object.freeze({ ok: true, ...rngOf(seed) });
}

function isSeed(value) {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 && value <= 0xffff_ffff;
}

// ---------------------------------------------------------------------------
// The schedule: the fault model, as data
// ---------------------------------------------------------------------------

const SCHEDULE_FIELDS = Object.freeze(['seed', 'rate', 'ops', 'clocks', 'maxDelayMs', 'faults']);
const FAULT_FIELDS = Object.freeze(['frame', 'op', 'delayMs']);

/**
 * emptySchedule is the schedule with no faults at all: the base case every
 * faulted run is compared against. It is data like any other schedule, so
 * `createSimulator({ schedule: emptySchedule({ seed: 7 }) })` is a run whose
 * only variable is the seed.
 *
 * @param {{ seed?: number }} [options]
 * @returns {object} a frozen schedule
 */
export function emptySchedule(options = {}) {
  const seed = options.seed === undefined ? DEFAULT_SEED : options.seed;
  if (!isSeed(seed)) return refuse(REFUSAL.BAD_SEED, 'seed');
  return Object.freeze({
    seed,
    rate: 0,
    ops: Object.freeze([]),
    clocks: Object.freeze({}),
    maxDelayMs: DEFAULT_MAX_DELAY_MS,
    faults: Object.freeze([]),
  });
}

/**
 * scheduleFromSeed draws a fault schedule from a seed: the same seed always
 * draws the same schedule, and the schedule is data, so a failing run can be
 * replayed from the number alone.
 *
 *     scheduleFromSeed(seed, { actors, ops, rate, maxDelayMs })
 *
 * `actors` names the actors whose clocks the schedule skews (the phone and the
 * consumers, by id); each gets a skew drawn within `±MAX_CLOCK_SKEW`, which is
 * the tolerance the lease's rule and the mailbox's future bound are both
 * stated in. `ops` and `rate` choose the wire's per-frame faults: each frame
 * is faulted with probability `rate`, and the op is drawn from `ops`.
 *
 * @param {unknown} seed
 * @param {{ actors?: string[], ops?: string[], rate?: number, maxDelayMs?: number }} [options]
 * @returns {object} a frozen schedule, or a refusal
 */
export function scheduleFromSeed(seed, options = {}) {
  if (!isSeed(seed)) return refuse(REFUSAL.BAD_SEED, 'seed');
  const read = readOptional(options, ['actors', 'ops', 'rate', 'maxDelayMs'], 'options');
  if (!read.ok) return read;
  const values = read.values;

  const actors = values.actors === undefined ? [] : values.actors;
  if (!Array.isArray(actors)) return refuse(REFUSAL.BAD_TYPE, 'actors');

  const ops = values.ops === undefined ? WIRE_OPS : values.ops;
  const readOps = readOpsList(ops, 'ops');
  if (!readOps.ok) return readOps;

  const rate = values.rate === undefined ? DEFAULT_RATE : values.rate;
  if (!isRate(rate)) return refuse(REFUSAL.BAD_RATE, 'rate');

  const maxDelayMs = values.maxDelayMs === undefined ? DEFAULT_MAX_DELAY_MS : values.maxDelayMs;
  if (!isDelay(maxDelayMs)) return refuse(REFUSAL.BAD_DELAY, 'maxDelayMs');

  // One draw per actor, in the order the caller named them, so the schedule is
  // a function of (seed, actors) and not of a set's iteration order.
  const clocks = {};
  for (const actor of actors) {
    if (typeof actor !== 'string' || actor === '') return refuse(REFUSAL.BAD_TYPE, 'actors');
    const rng = rngOf(mix32(seed, hashString(actor)));
    const skew = rng.nextInt(2 * MAX_CLOCK_SKEW + 1) - MAX_CLOCK_SKEW;
    clocks[actor] = skew;
  }

  return Object.freeze({
    seed,
    rate,
    ops: readOps.ops,
    clocks: Object.freeze(clocks),
    maxDelayMs,
    faults: Object.freeze([]),
  });
}

/**
 * faultFor answers the fault a schedule applies to one frame, or null.
 *
 * It is a **pure function of the schedule and the frame's number**: an
 * explicit schedule looks the frame up in its `faults`, and a generated one
 * derives the decision from `(seed, frame)` — so asking for frame 9 before
 * frame 3 gives frame 9 the same answer, and a run can be replayed by asking
 * the same questions in the same order.
 *
 * @param {object} schedule
 * @param {number} frame the frame's number, from 1
 * @returns {object | null} a frozen fault, or null for a clean frame
 */
export function faultFor(schedule, frame) {
  const read = readSchedule(schedule);
  if (!read.ok) return null;
  const plan = read.schedule;
  if (plan.faults.length > 0) {
    const found = plan.faults.find((fault) => fault.frame === frame);
    return found === undefined ? null : found;
  }
  if (plan.rate <= 0 || plan.ops.length === 0) return null;
  const rng = rngOf(mix32(plan.seed, frame));
  if (!rng.chance(plan.rate)) return null;
  const op = rng.pick(plan.ops);
  if (op === FAULT.DELAY) {
    return Object.freeze({ frame, op, delayMs: 1 + rng.nextInt(plan.maxDelayMs) });
  }
  return Object.freeze({ frame, op });
}

/**
 * scheduleText is the schedule as one line of JSON: the replayable form, and
 * what a failure message prints. Its keys are written in a fixed order and its
 * `clocks` and `faults` are sorted, so two equal schedules have equal text —
 * including two that name the same faults in a different order, which is the
 * same schedule. Sorting the faults by frame is stable, so two faults on one
 * frame keep the order they were written in, which is the order `faultFor`
 * reads them in.
 *
 * @param {object} schedule
 * @returns {string} JSON, or '' for a schedule this module refuses
 */
export function scheduleText(schedule) {
  const read = readSchedule(schedule);
  if (!read.ok) return '';
  const plan = read.schedule;
  const clocks = {};
  for (const actor of Object.keys(plan.clocks).sort()) clocks[actor] = plan.clocks[actor];
  const faults = [...plan.faults].sort((left, right) => left.frame - right.frame);
  return JSON.stringify({
    seed: plan.seed,
    rate: plan.rate,
    ops: plan.ops,
    clocks,
    maxDelayMs: plan.maxDelayMs,
    faults,
  });
}

/**
 * parseScheduleText reads back what `scheduleText` wrote — a schedule a
 * failure message printed, replayed exactly.
 *
 * @param {unknown} text
 * @returns {object} a frozen schedule, or a refusal
 */
export function parseScheduleText(text) {
  if (typeof text !== 'string') return refuse(REFUSAL.BAD_SCHEDULE_TEXT, 'text');
  let parsed;
  try {
    parsed = JSON.parse(text);
  } catch {
    return refuse(REFUSAL.BAD_SCHEDULE_TEXT, 'text');
  }
  const read = readSchedule(parsed);
  return read.ok ? read.schedule : read;
}

/**
 * readSchedule validates a schedule and answers a frozen, canonical copy.
 *
 * A schedule is either **explicit** (a non-empty `faults` list, and `rate` 0)
 * or **generated** (`rate` and `ops`), never both: a frame's fault has one
 * source, or a run would have two answers to what happened to it.
 */
function readSchedule(value) {
  const read = readOptional(value, SCHEDULE_FIELDS, 'schedule');
  if (!read.ok) return read;
  const fields = read.values;
  for (const field of SCHEDULE_FIELDS) {
    if (fields[field] === undefined) return refuse(REFUSAL.MISSING_FIELD, field);
  }
  if (!isSeed(fields.seed)) return refuse(REFUSAL.BAD_SEED, 'seed');
  if (!isRate(fields.rate)) return refuse(REFUSAL.BAD_RATE, 'rate');
  if (!isDelay(fields.maxDelayMs)) return refuse(REFUSAL.BAD_DELAY, 'maxDelayMs');
  const ops = readOpsList(fields.ops, 'ops');
  if (!ops.ok) return ops;

  const clocks = readClocks(fields.clocks);
  if (!clocks.ok) return clocks;

  if (!Array.isArray(fields.faults)) return refuse(REFUSAL.BAD_TYPE, 'faults');
  const faults = [];
  for (const raw of fields.faults) {
    const fault = readFault(raw, fields.maxDelayMs);
    if (!fault.ok) return fault;
    faults.push(fault.fault);
  }
  if (faults.length > 0 && fields.rate > 0) return refuse(REFUSAL.MIXED_SCHEDULE, 'rate');

  return {
    ok: true,
    schedule: Object.freeze({
      seed: fields.seed,
      rate: fields.rate,
      ops: ops.ops,
      clocks: clocks.clocks,
      maxDelayMs: fields.maxDelayMs,
      faults: Object.freeze(faults),
    }),
  };
}

function readOpsList(value, where) {
  if (!Array.isArray(value)) return refuse(REFUSAL.BAD_OPS, where);
  const ops = [];
  for (const op of value) {
    if (typeof op !== 'string' || !WIRE_OPS.includes(op)) return refuse(REFUSAL.UNKNOWN_OP, where);
    if (ops.includes(op)) return refuse(REFUSAL.BAD_OPS, where);
    ops.push(op);
  }
  return { ok: true, ops: Object.freeze(ops) };
}

function readClocks(value) {
  if (!isPlainObject(value)) return refuse(REFUSAL.BAD_CLOCKS, 'clocks');
  const clocks = {};
  for (const actor of Object.keys(value)) {
    if (actor === '') return refuse(REFUSAL.BAD_CLOCKS, 'clocks');
    const skew = value[actor];
    if (typeof skew !== 'number' || !Number.isSafeInteger(skew)) {
      return refuse(REFUSAL.BAD_SKEW, 'clocks');
    }
    if (Math.abs(skew) > MAX_SKEW_MS) return refuse(REFUSAL.BAD_SKEW, 'clocks');
    clocks[actor] = skew;
  }
  return { ok: true, clocks: Object.freeze(clocks) };
}

function readFault(value, maxDelayMs) {
  const read = readOptional(value, FAULT_FIELDS, 'faults');
  if (!read.ok) return read;
  const fields = read.values;
  if (!Number.isSafeInteger(fields.frame) || fields.frame < 1) {
    return refuse(REFUSAL.BAD_FAULT, 'faults');
  }
  if (typeof fields.op !== 'string' || !WIRE_OPS.includes(fields.op)) {
    return refuse(REFUSAL.UNKNOWN_OP, 'faults');
  }
  if (fields.op === FAULT.DELAY) {
    if (!isDelay(fields.delayMs) || fields.delayMs > maxDelayMs) {
      return refuse(REFUSAL.BAD_DELAY, 'faults');
    }
    return { ok: true, fault: Object.freeze({ frame: fields.frame, op: fields.op, delayMs: fields.delayMs }) };
  }
  if (fields.delayMs !== undefined) return refuse(REFUSAL.UNKNOWN_FIELD, 'faults');
  return { ok: true, fault: Object.freeze({ frame: fields.frame, op: fields.op }) };
}

function isRate(value) {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= 1;
}

function isDelay(value) {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 1 && value <= MAX_FRAME_DELAY_MS;
}

/** A small, stable string hash, so an actor's id can seed its clock skew. */
function hashString(value) {
  let h = 0x811c9dc5;
  for (let index = 0; index < value.length; index += 1) {
    h = Math.imul(h ^ value.charCodeAt(index), 0x01000193) >>> 0;
  }
  return h;
}

// ---------------------------------------------------------------------------
// The clock
// ---------------------------------------------------------------------------

/**
 * manualClock is the injected clock: a number the caller moves. Nothing in
 * this module reads the machine's time, so a simulated hour costs nothing and
 * two runs of one schedule take the same simulated time.
 *
 * @param {number} [startMs]
 * @returns {{ now: () => number, advance: (ms: number) => number, set: (ms: number) => number }}
 */
export function manualClock(startMs = DEFAULT_START_MS) {
  let at = startMs;
  return Object.freeze({
    now: () => at,
    advance: (ms) => {
      at += ms;
      return at;
    },
    set: (ms) => {
      at = ms;
      return at;
    },
  });
}

// ---------------------------------------------------------------------------
// The store
// ---------------------------------------------------------------------------

/**
 * createStore builds the in-memory model of the mailbox database: the inbox,
 * the outbox, the heartbeat, the consumer lease and the mailbox's ledger.
 *
 *     createStore({ promptTtlMs, startMs?, windowMs?, maxSeen?, maxPerSender?,
 *                   futureToleranceMs?, leaseTtl? }) -> the store | a refusal
 *
 * The two state values are built by the modules that own them —
 * `mailbox.initialState` and `lease.emptyLeaseState` — and their refusals are
 * propagated verbatim, so a store cannot exist with a ledger or a lease the
 * modules would refuse. The lease and the ledger are committed together, by
 * `casCommit`, against a generation: they are one persisted value, which is
 * what the lease's README means by "the state is replaced only by
 * compare-and-swap".
 *
 * @param {object} [options]
 * @returns {object} the store, or a refusal
 */
export function createStore(options = {}) {
  const config = readConfig(options);
  if (!config.ok) return config;

  const ledger = initialState({
    now: config.values.startMs,
    promptTtlMs: config.values.promptTtlMs,
    ...(config.values.windowMs === undefined ? {} : { windowMs: config.values.windowMs }),
    ...(config.values.maxSeen === undefined ? {} : { maxSeen: config.values.maxSeen }),
    ...(config.values.maxPerSender === undefined ? {} : { maxPerSender: config.values.maxPerSender }),
    ...(config.values.futureToleranceMs === undefined
      ? {}
      : { futureToleranceMs: config.values.futureToleranceMs }),
  });
  if (!ledger.ok) return refuse(ledger.code, ledger.field);

  const lease = emptyLeaseState(config.values.leaseTtl);
  const readLeaseState = readLease(lease);
  if (!readLeaseState.ok) return refuse(readLeaseState.code, 'leaseTtl');

  const store = {
    ok: true,
    config: config.values,
    inbox: new Map(),
    outbox: new Map(),
    kinds: new Map(),
    heartbeat: null,
    lease,
    mailbox: ledger.state,
    generation: 0,
    writes: [],
  };
  return store;
}

/**
 * readSnapshot is one consumer's read of the store: the generation, the two
 * state values, the three message families and the heartbeat — copied, so that
 * what the consumer decided against cannot change under it.
 */
export function readSnapshot(store) {
  return Object.freeze({
    generation: store.generation,
    lease: store.lease,
    mailbox: store.mailbox,
    inbox: Object.freeze([...store.inbox.values()]),
    outbox: Object.freeze([...store.outbox.values()]),
    heartbeat: store.heartbeat,
  });
}

/**
 * casCommit replaces the store's lease and ledger, but only if the generation
 * is still the one the caller read. A stale generation is refused
 * (`cas-conflict`) and **nothing is written**: that refusal is the fence two
 * racing consumers meet, and the reason the at-most-once property survives
 * them.
 *
 * @param {object} store
 * @param {number} expected the generation the caller read
 * @param {{ lease: object, mailbox: object }} next
 * @returns {{ ok: true, generation: number } | { ok: false, code: string, field: string }}
 */
export function casCommit(store, expected, next) {
  if (!isPlainObject(next) || !isPlainObject(next.lease) || !isPlainObject(next.mailbox)) {
    return refuse(REFUSAL.BAD_TYPE, 'next');
  }
  if (expected !== store.generation) return refuse(REFUSAL.CAS_CONFLICT, 'generation');
  store.lease = next.lease;
  store.mailbox = next.mailbox;
  store.generation += 1;
  return Object.freeze({ ok: true, generation: store.generation });
}

// ---------------------------------------------------------------------------
// The simulator
// ---------------------------------------------------------------------------

/**
 * createSimulator builds a run: the store, the clock, the wire and its fault
 * schedule, and the actors that are put on it.
 *
 *     createSimulator({ promptTtlMs, schedule?, clock?, startMs?, windowMs?,
 *                       maxSeen?, maxPerSender?, futureToleranceMs?, leaseTtl? })
 *
 * `promptTtlMs` is required and is D118's mailbox TTL, exactly as the mailbox
 * module requires it: the simulator invents no number the owner has not
 * stated. The result is the simulator, or a refusal carrying a code.
 *
 * @param {object} [options]
 * @returns {object} the simulator, or a refusal
 */
export function createSimulator(options = {}) {
  const read = readOptional(options, [
    'promptTtlMs',
    'startMs',
    'windowMs',
    'maxSeen',
    'maxPerSender',
    'futureToleranceMs',
    'leaseTtl',
    'schedule',
    'clock',
  ], 'options');
  if (!read.ok) return read;
  const values = read.values;
  const { schedule: scheduleInput, clock: clockInput, ...configInput } = values;

  const config = readConfig(configInput);
  if (!config.ok) return config;

  let schedule;
  if (scheduleInput === undefined) {
    schedule = emptySchedule({ seed: DEFAULT_SEED });
    if (schedule.ok === false) return schedule;
  } else {
    const scheduleRead = readSchedule(scheduleInput);
    if (!scheduleRead.ok) return scheduleRead;
    schedule = scheduleRead.schedule;
  }

  const clock = clockInput === undefined ? manualClock(config.values.startMs) : clockInput;
  if (clock === null || typeof clock !== 'object' || typeof clock.now !== 'function') {
    return refuse(REFUSAL.NO_CLOCK, 'clock');
  }

  const store = createStore(config.values);
  if (store.ok !== true) return store;

  const actors = [];
  const logs = {
    passes: [],
    deliveries: [],
    actings: [],
    acks: [],
    commits: [],
    lease: [],
    faults: [],
    frames: [],
  };
  const wire = createWire({ schedule, clock });
  let pass = 0;

  const sim = {
    ok: true,
    clock,
    store,
    config: config.values,
    // The schedule the run is judged by, and the one it actually applied: the
    // second is the first with every drawn fault written out by frame number,
    // which is what a failure message prints and what replays it exactly.
    schedule: () => expandedSchedule(),
    scheduleText: () => scheduleText(expandedSchedule()),
    now: () => clock.now(),
    faults: () => Object.freeze([...logs.faults]),
    frames: () => Object.freeze([...logs.frames]),
    consumers: () => Object.freeze([...actors]),
    advance,
    drain,
    tick,
    run,
    report,
  };

  sim.phone = createPhone(sim);

  /**
   * consumer registers a consumer actor under an id. The id is the lease's
   * holder, so it is the lease's rule that decides whether it is a name at
   * all: a blank or look-alike id is refused when it tries to take the lease,
   * not silently accepted here.
   */
  sim.consumer = (id, options = {}) => {
    if (typeof id !== 'string' || id === '') return refuse(REFUSAL.BAD_TYPE, 'id');
    if (id === PHONE) return refuse(REFUSAL.DUPLICATE_ACTOR, 'id');
    if (actors.some((actor) => actor.id === id)) return refuse(REFUSAL.DUPLICATE_ACTOR, 'id');
    const consumer = createConsumer(sim, id, options);
    if (consumer.ok === false) return consumer;
    actors.push(consumer);
    return consumer;
  };

  function stamp(actor) {
    return clock.now() + (schedule.clocks[actor] === undefined ? 0 : schedule.clocks[actor]);
  }

  function advance(ms) {
    if (!Number.isSafeInteger(ms) || ms < 0) return refuse(REFUSAL.BAD_MILLISECONDS, 'ms');
    clock.advance(ms);
    drain();
    return Object.freeze({ ok: true, now: clock.now() });
  }

  function drain() {
    return wire.drain();
  }

  /**
   * tick is one round: the wire delivers what has arrived, the phone re-sends
   * and speaks, and every registered consumer takes its turn. The consumers'
   * turns are interleaved — all of them read the store, all of them decide,
   * then all of them commit — because that is the interleaving the
   * compare-and-swap exists for. A consumer whose commit loses the race acts
   * on nothing.
   */
  function tick() {
    pass += 1;
    drain();
    sim.phone.pass();
    drain();
    for (const consumer of actors) consumer.deliverPlan();
    for (const consumer of actors) consumer.deliverCommit();
    for (const consumer of actors) consumer.actPlan();
    for (const consumer of actors) consumer.actCommit();
    drain();
    return Object.freeze({ ok: true, pass });
  }

  /**
   * run takes `ticks` turns, moving the clock by `intervalMs` between them.
   * It is the whole script the property tests drive.
   */
  function run(options = {}) {
    const readRun = readOptional(options, ['ticks', 'intervalMs'], 'run');
    if (!readRun.ok) return readRun;
    const ticks = readRun.values.ticks === undefined ? 1 : readRun.values.ticks;
    const intervalMs = readRun.values.intervalMs === undefined ? 0 : readRun.values.intervalMs;
    if (!Number.isSafeInteger(ticks) || ticks < 0 || ticks > MAX_TICKS) {
      return refuse(REFUSAL.TOO_MANY_TICKS, 'ticks');
    }
    if (!Number.isSafeInteger(intervalMs) || intervalMs < 0) {
      return refuse(REFUSAL.BAD_MILLISECONDS, 'intervalMs');
    }
    for (let index = 0; index < ticks; index += 1) {
      if (index > 0 && intervalMs > 0) advance(intervalMs);
      tick();
    }
    return Object.freeze({ ok: true, ticks, now: clock.now() });
  }

  /**
   * report is the run as one frozen, JSON-serialisable value: what was
   * delivered, what was acted on, what was committed, what the wire did, and
   * what the store holds. The property tests assert on it, and a failure
   * prints it beside the seed.
   */
  function report() {
    return Object.freeze({
      seed: schedule.seed,
      scheduleText: scheduleText(expandedSchedule()),
      passes: Object.freeze([...logs.passes]),
      deliveries: Object.freeze([...logs.deliveries]),
      actings: Object.freeze([...logs.actings]),
      acks: Object.freeze([...logs.acks]),
      commits: Object.freeze([...logs.commits]),
      lease: Object.freeze([...logs.lease]),
      faults: Object.freeze([...logs.faults]),
      frames: Object.freeze([...logs.frames]),
      store: Object.freeze({
        inbox: Object.freeze([...store.inbox.values()]),
        outbox: Object.freeze([...store.outbox.values()]),
        heartbeat: store.heartbeat,
        lease: store.lease,
        mailbox: store.mailbox,
        writes: Object.freeze([...store.writes]),
      }),
      phone: Object.freeze({
        written: Object.freeze([...sim.phone.written()]),
        spoken: Object.freeze([...sim.phone.spoken()]),
        pending: Object.freeze([...sim.phone.pending()]),
      }),
    });
  }

  function expandedSchedule() {
    return Object.freeze({
      seed: schedule.seed,
      rate: 0,
      ops: schedule.ops,
      clocks: schedule.clocks,
      maxDelayMs: schedule.maxDelayMs,
      faults: Object.freeze(logs.faults.map((fault) => fault.applied)),
    });
  }

  // The wire: the only way anything reaches the store. Every frame is
  // numbered, and the schedule's fault for that number is drawn when the frame
  // is sent, so the schedule is a function of the frame's number and not of
  // the wall clock or of how many frames came before it in another run.
  function createWire({ schedule: plan, clock: wireClock }) {
    let seq = 0;
    let queue = [];

    function send(frame) {
      seq += 1;
      const applied = faultFor(plan, seq);
      const record = Object.freeze({
        seq,
        op: frame.op,
        label: frame.label,
        from: frame.from,
        at: frame.at,
      });
      if (applied !== null) logs.faults.push(Object.freeze({ seq, op: applied.op, applied }));
      const entry = {
        seq,
        op: frame.op,
        from: frame.from,
        at: frame.at,
        payload: frame.payload,
        arriveAt: wireClock.now(),
        reorder: false,
        copy: 1,
        dropped: false,
      };
      if (applied === null) {
        queue.push(entry);
      } else if (applied.op === FAULT.DROP) {
        entry.dropped = true;
      } else if (applied.op === FAULT.DELAY) {
        entry.arriveAt += applied.delayMs;
        queue.push(entry);
      } else if (applied.op === FAULT.REORDER) {
        entry.reorder = true;
        queue.push(entry);
      } else if (applied.op === FAULT.DUPLICATE) {
        queue.push(entry);
        queue.push({ ...entry, copy: 2 });
      }
      logs.frames.push(Object.freeze({ ...record, fault: applied === null ? null : applied.op }));
      return record;
    }

    function drain() {
      const now = wireClock.now();
      const due = queue
        .filter((entry) => entry.arriveAt <= now)
        .sort(
          (left, right) =>
            left.arriveAt - right.arriveAt || left.seq - right.seq || left.copy - right.copy,
        );
      // A reorder swaps a frame with the frame beside it, in the order the
      // drain would otherwise have used: the two arrive the other way round.
      for (let index = 0; index < due.length - 1; index += 1) {
        if (due[index].reorder && !due[index + 1].reorder) {
          const swapped = due[index];
          due[index] = due[index + 1];
          due[index + 1] = swapped;
          index += 1;
        }
      }
      queue = queue.filter((entry) => entry.arriveAt > now);
      for (const entry of due) applyFrame(store, entry);
      return Object.freeze(due.map((entry) => entry.seq));
    }

    return { send, drain };
  }

  function applyFrame(target, frame) {
    if (frame.op === 'inbox-write') {
      // A write is a **merge**, not a replacement, and the field it preserves
      // is the ack. The phone re-sends a message until the outbox answers it,
      // so the same frame can arrive again after a consumer has written the
      // ack into that very record — and a replacement would clear the ack while
      // the ledger still holds the record as `acted`/`acked`. Nothing would
      // repair that: the ledger's ack step only acks a record it has acted on,
      // so the message would look unacked to every later pass and be delivered
      // for ever. The ack is not the phone's field to clear, which is what
      // makes a duplicated write idempotent rather than merely harmless.
      const held = target.inbox.get(frame.payload.id);
      const acked = held !== undefined && held.acked === true;
      target.inbox.set(
        frame.payload.id,
        acked ? Object.freeze({ ...frame.payload, acked: true }) : frame.payload,
      );
    } else if (frame.op === 'reply-write') {
      target.outbox.set(frame.payload.id, frame.payload);
    } else if (frame.op === 'ack-write') {
      const held = target.inbox.get(frame.payload.id);
      if (held !== undefined && held.acked !== true) {
        target.inbox.set(frame.payload.id, Object.freeze({ ...held, acked: true }));
      }
    } else if (frame.op === 'heartbeat-write') {
      target.heartbeat = frame.payload;
    }
    target.writes.push(Object.freeze({ seq: frame.seq, op: frame.op, id: frame.payload.id ?? null, at: frame.at }));
  }

  function send(op, label, from, at, payload) {
    return wire.send({ op, label, from, at, payload });
  }

  return sim;

  // -------------------------------------------------------------------------
  // The phone
  // -------------------------------------------------------------------------

  function createPhone(simulator) {
    let counter = 0;
    const written = new Map();
    const spoken = new Set();

    const phone = {
      id: PHONE,

      /**
       * writeInbox writes one message into the phone's own inbox. The id is
       * the caller's or minted here (`p-<n>`), and the message is the
       * schema's: a message the schema refuses is refused here, with the
       * schema's own code, and nothing is sent.
       */
      writeInbox(input) {
        const readMessage = readOptional(input, ['id', 'text', 'kind'], 'message');
        if (!readMessage.ok) return readMessage;
        const fields = readMessage.values;
        if (fields.text === undefined) return refuse(REFUSAL.MISSING_FIELD, 'text');
        let id = fields.id;
        if (id === undefined) {
          counter += 1;
          id = `p-${counter}`;
        }
        const kind = fields.kind === undefined ? KIND.COMMAND : fields.kind;
        if (kind !== KIND.COMMAND && kind !== KIND.PROMPT) return refuse(REFUSAL.BAD_TYPE, 'kind');
        const message = Object.freeze({
          id,
          sender: PHONE,
          text: fields.text,
          at: stamp(PHONE),
          mac: PLACEHOLDER_MAC,
        });
        const validated = validateMessage(message);
        if (!validated.ok) return refuse(validated.code, validated.field);
        written.set(id, validated.message);
        store.kinds.set(id, kind);
        send('inbox-write', 'inbox', PHONE, validated.message.at, validated.message);
        return Object.freeze({ ok: true, message: validated.message });
      },

      /** pass re-sends what the outbox has not answered, then speaks what it holds. */
      pass() {
        for (const message of written.values()) {
          if (!store.outbox.has(replyId(message.id))) {
            // The same message, `at` included: a retry is not a new message.
            send('inbox-write', 'inbox', PHONE, message.at, message);
          }
        }
        for (const reply of store.outbox.values()) spoken.add(reply.id);
      },

      /** The ids the phone has spoken: the outbox messages it has read. */
      spoken: () => [...spoken],
      /** The messages the phone has written. */
      written: () => [...written.values()],
      /** The messages the outbox has not answered yet. */
      pending: () => [...written.values()].filter((message) => !store.outbox.has(replyId(message.id))),
    };
    return Object.freeze(phone);
  }

  // -------------------------------------------------------------------------
  // A consumer
  // -------------------------------------------------------------------------

  function createConsumer(simulator, id, options) {
    const held = readOptional(options, [], 'options');
    if (!held.ok) return held;
    // What this consumer has already written and not yet seen in the store: a
    // reply is re-sent unchanged, so a retry is never a new message.
    const replies = new Map();
    const acks = new Map();
    // The pass's plan, and which half of the turn it belongs to. A plan is
    // built in one method and committed in the next, so the stage is what says
    // whether the plan is a delivery waiting to be committed or the ledger of
    // a delivery that already was.
    let plan = null;

    const consumer = {
      id,

      /** The mailbox's ledger, as this consumer last committed it. */
      ledger: () => store.mailbox,

      /** deliverPlan reads the store and decides against that snapshot. */
      deliverPlan() {
        plan = null;
        const snapshot = readSnapshot(store);
        const lease = holdLease(snapshot, id, stamp(id));
        logs.lease.push(
          Object.freeze({ pass, consumer: id, held: lease.holds, code: lease.code, holder: lease.holder }),
        );
        if (!lease.holds) return;
        // The persisted ledger, carried across passes: this is the dedupe. A
        // consumer that re-derived the ledger from nothing would re-accept
        // every redelivery and act on it a second time.
        let state = snapshot.mailbox;
        const delivered = [];
        let unacked = 0;
        for (const message of snapshot.inbox) {
          if (message.acked === true) continue;
          unacked += 1;
          const kind = store.kinds.get(message.id) === undefined ? KIND.COMMAND : store.kinds.get(message.id);
          const result = step(state, { type: EVENT.DELIVER, now: clock.now(), message, kind });
          if (!result.ok) {
            delivered.push(
              Object.freeze({
                pass,
                consumer: id,
                id: message.id,
                sender: message.sender,
                outcome: 'refused',
                code: result.code,
              }),
            );
            continue;
          }
          state = result.state;
          delivered.push(
            Object.freeze({
              pass,
              consumer: id,
              id: message.id,
              sender: message.sender,
              outcome: result.outcome,
            }),
          );
        }
        plan = { stage: 'deliver', snapshot, lease, state, delivered, unacked, actings: [], acks: [] };
      },

      /** deliverCommit commits the lease and the deliveries, or acts on nothing. */
      deliverCommit() {
        if (plan === null || plan.stage !== 'deliver') {
          logs.passes.push(
            Object.freeze({ pass, consumer: id, held: false, ok: false, unacked: null, delivered: null }),
          );
          return;
        }
        const result = casCommit(store, plan.snapshot.generation, {
          lease: plan.lease.next,
          mailbox: plan.state,
        });
        logs.commits.push(Object.freeze({ pass, consumer: id, phase: 'deliver', ok: result.ok }));
        logs.passes.push(
          Object.freeze({
            pass,
            consumer: id,
            held: true,
            ok: result.ok,
            unacked: plan.unacked,
            delivered: result.ok ? plan.delivered.length : 0,
          }),
        );
        if (!result.ok) {
          plan = null;
          return;
        }
        for (const record of plan.delivered) logs.deliveries.push(record);
        // The heartbeat is the holder's write (D113b): written once a pass,
        // by the consumer that holds the lease and by no one else.
        send('heartbeat-write', 'heartbeat', id, stamp(id), Object.freeze({ at: stamp(id) }));
      },

      /** actPlan decides what the oldest un-acted deliveries call for. */
      actPlan() {
        // Only a consumer whose delivery commit won has a plan left to act
        // on: a lost commit nulls it, which is the whole point of the fence.
        if (plan === null || plan.stage !== 'deliver') return;
        const snapshot = readSnapshot(store);
        const lease = holdLease(snapshot, id, stamp(id));
        if (!lease.holds) {
          plan = null;
          return;
        }
        let state = snapshot.mailbox;
        const actings = [];
        for (;;) {
          const oldest = state.seen.find((entry) => entry.status === STATUS.ACCEPTED);
          if (oldest === undefined) break;
          const result = step(state, { type: EVENT.ACT, now: clock.now() });
          if (!result.ok) break;
          state = result.state;
          if (result.outcome === OUTCOME.ACTED) {
            actings.push(
              Object.freeze({
                pass,
                consumer: id,
                id: result.id,
                sender: result.sender,
                at: oldest.at,
                outcome: OUTCOME.ACTED,
                action: 'reply',
              }),
            );
          } else if (result.outcome === OUTCOME.STALE_PENDING) {
            // The work is late. The safe path is to do nothing with it: what a
            // prompt's expiry *delivers* is P6.5's and P6.7's rule, and those
            // are proposed and NOT APPROVED, so this harness invents none.
            actings.push(
              Object.freeze({
                pass,
                consumer: id,
                id: result.id,
                sender: result.sender,
                at: oldest.at,
                outcome: OUTCOME.STALE_PENDING,
                action: null,
              }),
            );
          } else {
            break;
          }
        }

        // The ledger's own ack: the oldest record this consumer acted on, and
        // only once the store shows that message acked. The ack is the ack of
        // the message the store acks, never of whatever is oldest.
        const acted = state.seen.find((entry) => entry.status === STATUS.ACTED);
        const acks = [];
        if (acted !== undefined && isAcked(snapshot.inbox, acted.id)) {
          const result = step(state, { type: EVENT.ACK, now: clock.now() });
          if (result.ok) {
            state = result.state;
            acks.push(Object.freeze({ pass, consumer: id, id: result.id, outcome: result.outcome }));
          }
        }

        plan = { stage: 'act', snapshot, lease, state, delivered: [], unacked: 0, actings, acks };
      },

      /** actCommit commits the ledger, and only then performs the actions. */
      actCommit() {
        if (plan === null || plan.stage !== 'act') return;
        const result = casCommit(store, plan.snapshot.generation, {
          lease: plan.lease.next,
          mailbox: plan.state,
        });
        logs.commits.push(Object.freeze({ pass, consumer: id, phase: 'act', ok: result.ok }));
        if (!result.ok) {
          plan = null;
          return;
        }
        for (const record of plan.actings) logs.actings.push(record);
        for (const record of plan.acks) logs.acks.push(record);
        // The writes this consumer owes the store, built now that the commit
        // has won: a consumer that lost the race owes nothing, so it builds
        // nothing.
        //
        // A reply is owed for a record this consumer **performed** on, and for
        // no other. The ledger cannot tell the two apart: the mailbox moves a
        // stale record to `acted` exactly as it moves a performed one, and
        // reports the difference in the outcome alone (its README: "what late
        // means — perform it anyway, or tell the sender it expired — is the
        // consumer's to decide"), so no holder can re-derive "was this
        // performed?" from the store. A reply for every consumed record would
        // therefore invent an answer to a prompt the harness has no rule for —
        // what a late prompt *delivers* is P6.5's and P6.7's, both proposed and
        // NOT APPROVED. The obligation lives here instead, with the consumer
        // that performed, and is re-sent until the store shows it.
        for (const acting of plan.actings) {
          if (acting.outcome !== OUTCOME.ACTED) continue;
          const key = replyId(acting.id);
          if (!store.outbox.has(key) && !replies.has(key)) {
            const validated = validateMessage(
              Object.freeze({
                id: key,
                sender: id,
                text: `answer to ${acting.id}`,
                at: stamp(id),
                mac: PLACEHOLDER_MAC,
              }),
            );
            if (validated.ok) replies.set(key, validated.message);
          }
        }
        // The ack is the other half, and it **is** the ledger's to say: an
        // `acted` record is one an ack is still owed for (the mailbox's own
        // table), so any holder owes it, for a stale record exactly as for a
        // performed one. The ack says the delivery is consumed, not that the
        // work was done, which is why the safe path acks an expired prompt and
        // still performs nothing with it.
        for (const entry of plan.state.seen) {
          if (entry.status !== STATUS.ACTED) continue;
          if (!isAcked(store.inbox, entry.id) && !acks.has(entry.id)) {
            acks.set(entry.id, Object.freeze({ id: entry.id }));
          }
        }
        // Every reply and every ack this consumer owes and has not seen land:
        // re-sent unchanged until the store shows it, which is what makes a
        // dropped write a redelivery rather than a loss.
        for (const reply of replies.values()) {
          if (!store.outbox.has(reply.id)) send('reply-write', 'reply', id, reply.at, reply);
        }
        for (const ack of acks.values()) {
          if (!isAcked(store.inbox, ack.id)) {
            send('ack-write', 'ack', id, stamp(id), Object.freeze({ id: ack.id }));
          }
        }
        for (const [key, reply] of [...replies]) if (store.outbox.has(key)) replies.delete(key);
        for (const [key] of [...acks]) if (isAcked(store.inbox, key)) acks.delete(key);
        plan = null;
      },

      /** pass is one consumer's whole turn, for a caller driving it by hand. */
      pass() {
        consumer.deliverPlan();
        consumer.deliverCommit();
        consumer.actPlan();
        consumer.actCommit();
      },
    };
    return Object.freeze(consumer);
  }

  /**
   * holdLease decides, from one snapshot, whether this consumer may act: it
   * holds the lease if the take succeeds, and also if the take is refused
   * `already-claimed` **by this consumer** — the lease's own rule is that a
   * holder renews by releasing, never by re-taking, so a holder's own take is
   * answered that way. Any other refusal (another holder, an unreadable
   * claim) means this consumer does not act, and writes nothing.
   */
  function holdLease(snapshot, id, at) {
    const result = takeLease(snapshot.lease, { holder: id, at });
    if (result.ok) return { holds: true, next: result.state, code: null, holder: id };
    if (result.code === LEASE_CODES.ALREADY_CLAIMED && result.existing.holder === id) {
      return { holds: true, next: snapshot.lease, code: null, holder: id };
    }
    return {
      holds: false,
      next: snapshot.lease,
      code: result.code,
      holder: result.existing === undefined ? null : result.existing.holder,
    };
  }
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

/**
 * The id of the reply that answers a message. **This is the harness's own
 * convention, not a rule**: P0.2's schema has no `reply_to` (P6.3 proposes
 * one, and P6.3 is NOT APPROVED), so nothing in the message links a reply to
 * what it answers, and the simulator makes the link out of the id.
 */
export function replyId(id) {
  return `r-${id}`;
}

/**
 * isAcked reads the ack out of the inbox, whether it is the store's own map
 * (keyed by id) or a snapshot's copy of it (an array). The inbox is the only
 * place an ack lives: the ledger learns of it, it never invents it.
 */
function isAcked(inbox, id) {
  if (inbox instanceof Map) {
    const held = inbox.get(id);
    return held !== undefined && held.acked === true;
  }
  return inbox.some((message) => message.id === id && message.acked === true);
}

function readConfig(options) {
  const read = readOptional(options, [
    'startMs',
    'promptTtlMs',
    'windowMs',
    'maxSeen',
    'maxPerSender',
    'futureToleranceMs',
    'leaseTtl',
  ], 'options');
  if (!read.ok) return read;
  const values = read.values;
  // `startMs`, `promptTtlMs` and `leaseTtl` are handed to the modules that own
  // them without a second opinion here: `initialState` refuses a missing or
  // out-of-range prompt TTL and an impossible `now`, and `readLease` refuses a
  // TTL the lease would not hold — each with its own code. A guard of this
  // harness's own would be re-implementing a rule it exists to compose, and
  // would answer with the harness's code instead of the module's.
  const startMs = values.startMs === undefined ? DEFAULT_START_MS : values.startMs;
  const leaseTtl = values.leaseTtl;
  return {
    ok: true,
    values: Object.freeze({
      startMs,
      promptTtlMs: values.promptTtlMs,
      ...(values.windowMs === undefined ? {} : { windowMs: values.windowMs }),
      ...(values.maxSeen === undefined ? {} : { maxSeen: values.maxSeen }),
      ...(values.maxPerSender === undefined ? {} : { maxPerSender: values.maxPerSender }),
      ...(values.futureToleranceMs === undefined ? {} : { futureToleranceMs: values.futureToleranceMs }),
      ...(leaseTtl === undefined ? {} : { leaseTtl }),
    }),
  };
}

/**
 * readOptional reads exactly the named fields of a plain object, refusing an
 * unknown one rather than dropping it: the field set is the version, exactly
 * as the two modules this harness composes have it.
 */
function readOptional(value, names, where) {
  if (!isPlainObject(value)) return refuse(REFUSAL.NOT_AN_OBJECT, where);
  const values = {};
  for (const key of Object.keys(value)) {
    if (!names.includes(key)) return refuse(REFUSAL.UNKNOWN_FIELD, where);
    values[key] = value[key];
  }
  return { ok: true, values };
}

function isPlainObject(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}
