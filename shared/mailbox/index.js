// The mailbox's state machine (mobile plan, Task P1.1).
//
// One pure function of a state and an event:
//
//     step(state, event) -> { ok: true, outcome, phase, id, sender, state }
//                        |  { ok: false, code, field }
//
// Why a state machine, and why now (D110, answered 2026-10-17): Voice D49
// promised idempotent delivery, and F19 is the list of what the tree does not
// have — no message id, no ordering, no idempotency, no replay protection, no
// ack. D110 supersedes D49 by meeting the standard D49 named, and writes the
// missing half down: an **explicit message id**, **at-least-once** delivery
// with **dedupe on the consumer's side**, a consumer lease (P1.2's, not this
// module's), an **ack**, and **replay protection**. This module is that half's
// decision procedure: given what the mailbox has seen and one event, what
// happens next, and what is refused.
//
// The four phases, and what each one says:
//
//     idle      nothing is waiting; the ledger holds only acked records
//     pending   a delivery has arrived that nobody has acted on yet
//     acted     every delivery has been acted on; an ack is still owed
//     closed    the mailbox is done; nothing is accepted, acted on or acked
//
// `pending` and `acted` are not counters: they are read off the ledger, and a
// state whose declared phase disagrees with its ledger is refused
// (`invalid-state`) rather than trusted. A phase that could be two things at
// once would make the machine's next answer a guess.
//
// **The ledger.** `seen` is the dedupe ledger: one frozen record per
// `(sender, id)` — `{ sender, id, at, status }` — in a canonical order
// (`at`, then `sender`, then `id`, compared by code unit, never by locale).
// Two deliveries of the same pair in either order therefore leave the
// *identical* state, which is what makes a redelivery answerable at all. The
// record holds `at` and a status, never the message's text: a ledger that
// carried text would be a second copy of the mailbox, and D113b's MAC — the
// phone's — is what protects a message's contents, not this table.
//
// **Bounded, and safe to bound.** The ledger has a cap (`maxSeen`) and a
// per-sender quota (`maxPerSender`), and the rules that keep them from being a
// hole in the replay fence are:
//
//   * a delivery at or above the cap is REFUSED (`dedupe-full`) unless an
//     entry is evictable, and one whose sender already holds its quota is
//     REFUSED (`sender-full`). Nothing that is not acked is ever dropped, to
//     make room or for any other reason: dropping an un-acked record is either
//     the "acted on twice" failure dedupe is for, or work lost silently;
//   * an entry is evictable only when it is **acked** and **strictly outside
//     the replay window**. Outside the window is the load-bearing half: a
//     redelivery of an evicted message is refused as a replay by the window
//     rule before the ledger is consulted, so eviction cannot re-admit a
//     replay;
//   * a record is consulted whatever its age: the same `(sender, id)` with the
//     same `at` is a `duplicate`, and with a different `at` it is
//     `id-conflict` — the id is the message's identity, so a second message
//     under it is an ambiguity, never a new delivery, for as long as the
//     ledger remembers the first;
//   * an accepted record that is still un-acted when the window passes it is
//     kept, and `act` answers `stale-pending` for it rather than `acted`: the
//     work is surfaced as late, never dropped.
//
// **Time is injected.** Every event carries the `now` it is to be judged at,
// and the machine reads no clock of its own: `now` is the caller's, anchored
// to the server's timestamp (D117), and the window is a function of `at` and
// that `now`. A `now` earlier than the state's own is refused
// (`now-went-backwards`), which is what lets the eviction argument above be
// sound — a window that could run in reverse would make "outside the window"
// mean nothing.
//
// **Fail closed.** An unknown field, a wrong type, an accessor property, an
// out-of-range number, a negative zero, a ledger that is not a ledger and an
// unknown state or event are each a REFUSAL with a code of its own, never a
// coerced value and never a guess. A refusal names the *field* at fault by a
// fixed path this module chose — never the value it refused, and never a key
// name the input chose — so a refusal can be logged.
//
// The message itself is P0.2's: this module calls `validateMessage` and
// propagates the schema's codes verbatim (prefixed with `message.`), because
// two validators that disagree about what a message is would be worse than
// either one alone. This module adds only what the schema cannot decide —
// whether an `at` is *too old*, and what the mailbox does about it.
//
// Nothing here reads a clock, a file, the network or a random source, and
// nothing here mutates what it is handed: the inputs are the whole input, and
// the answer is a frozen value. `shared/README.md` is the package's contract,
// `../mailbox-schema/` is the schema this module speaks, and
// `../../docs/mobile/implementation-plan.md`'s P1.1 is this module's task.

import { MAX_CLOCK_SKEW, MAX_TIMESTAMP } from '../lease/index.js';
import {
  FIELDS as MESSAGE_FIELDS,
  ID_MAX,
  REFUSALS,
  SENDER_MAX,
  validateMessage,
} from '../mailbox-schema/index.js';

/**
 * The four phases. Nothing else is a phase: a state carrying an unlisted one is
 * refused with `unknown-state` rather than treated as the nearest match.
 */
export const STATE = Object.freeze({
  IDLE: 'idle',
  PENDING: 'pending',
  ACTED: 'acted',
  CLOSED: 'closed',
});

/** The four events. An event whose `type` is not one of these is refused. */
export const EVENT = Object.freeze({
  DELIVER: 'deliver',
  ACT: 'act',
  ACK: 'ack',
  CLOSE: 'close',
});

/**
 * The kinds of message, each with its own acceptance window (owner ruling,
 * 2026-10-17, on the replay window against D168):
 *
 *   command  cancel, merge, launch, confirm — the replay window (`windowMs`,
 *            six hours by default), unchanged; older is `replay`
 *   prompt   a queued prompt — its own TTL (`promptTtlMs`, D118's mailbox
 *            TTL); older is `expired`. A consumer offline longer than six
 *            hours but inside the TTL still accepts it; past the TTL it has
 *            expired (the ruling supersedes D168's "never expire")
 *
 * P0.2's schema has no kind field yet, so the kind travels on the `deliver`
 * event (the consumer's assertion about which kind it is delivering) and is
 * written into the ledger record. It defaults to `command`, the stricter of
 * the two windows. P6.3 adds the kind to the message itself; then the event's
 * field goes, and the kind is read from the validated message.
 */
export const KIND = Object.freeze({
  COMMAND: 'command',
  PROMPT: 'prompt',
});

/**
 * The status of a ledger record. It is the consumer's own bookkeeping, not the
 * message's `acked` field: D113 gives the phone the write of the `acked` child,
 * and this status is what the consumer has done about a delivery so far.
 */
export const STATUS = Object.freeze({
  ACCEPTED: 'accepted',
  ACTED: 'acted',
  ACKED: 'acked',
});

/**
 * The outcomes a step can answer with. An outcome is not a phase: `duplicate`
 * is what happened to an event, and the phase says what the mailbox holds.
 */
export const OUTCOME = Object.freeze({
  ACCEPTED: 'accepted',
  DUPLICATE: 'duplicate',
  ACTED: 'acted',
  // `act` on an accepted record the window has already passed: it is acted on
  // (once, like any other), and the outcome says it is late, so a consumer
  // can decide what late means for it rather than lose it silently.
  STALE_PENDING: 'stale-pending',
  ACKED: 'acked',
  NOTHING_TO_ACT: 'nothing-to-act',
  NOTHING_TO_ACK: 'nothing-to-ack',
  CLOSED: 'closed',
});

/**
 * The refusal codes. One per distinct way a step can fail, because "the
 * message was too old" and "the mailbox was handed something it could not
 * read" are different facts, and one code for both loses the second one. The
 * schema's own codes are propagated as they are (see `INPUT_REFUSALS` for the
 * ones this module raises itself).
 */
export const REFUSAL = Object.freeze({
  // Reading the inputs.
  NOT_AN_OBJECT: 'not-an-object',
  ACCESSOR_PROPERTY: 'accessor-property',
  UNKNOWN_FIELD: 'unknown-field',
  MISSING_FIELD: 'missing-field',
  UNKNOWN_STATE: 'unknown-state',
  UNKNOWN_EVENT: 'unknown-event',
  INVALID_STATE: 'invalid-state',
  INVALID_NOW: 'invalid-now',
  NOW_WENT_BACKWARDS: 'now-went-backwards',
  INVALID_WINDOW: 'invalid-window',
  INVALID_MAX_SEEN: 'invalid-max-seen',
  INVALID_MAX_PER_SENDER: 'invalid-max-per-sender',
  INVALID_PROMPT_TTL: 'invalid-prompt-ttl',
  INVALID_FUTURE_TOLERANCE: 'invalid-future-tolerance',
  UNKNOWN_KIND: 'unknown-kind',
  INVALID_SEEN: 'invalid-seen',
  INVALID_SEEN_ENTRY: 'invalid-seen-entry',
  DUPLICATE_SEEN_ENTRY: 'duplicate-seen-entry',
  UNSORTED_SEEN: 'unsorted-seen',
  // The delivery.
  NEGATIVE_ZERO: 'negative-zero',
  ZERO_TIMESTAMP: 'zero-timestamp',
  REPLAY: 'replay',
  EXPIRED: 'expired',
  FUTURE_TIMESTAMP: 'future-timestamp',
  ID_CONFLICT: 'id-conflict',
  DEDUPE_FULL: 'dedupe-full',
  SENDER_FULL: 'sender-full',
  // The mailbox.
  MAILBOX_CLOSED: 'mailbox-closed',
});

/**
 * The refusals that come from reading a state or an event — the ones that are
 * the same whichever event arrived. They are listed separately from the
 * transition table because they belong to no cell: every cell can answer with
 * them, and a table that repeated them sixteen times would be a table nobody
 * reads.
 */
export const INPUT_REFUSALS = Object.freeze([
  REFUSAL.NOT_AN_OBJECT,
  REFUSAL.ACCESSOR_PROPERTY,
  REFUSAL.UNKNOWN_FIELD,
  REFUSAL.MISSING_FIELD,
  REFUSAL.UNKNOWN_STATE,
  REFUSAL.UNKNOWN_EVENT,
  REFUSAL.INVALID_STATE,
  REFUSAL.INVALID_NOW,
  REFUSAL.NOW_WENT_BACKWARDS,
  REFUSAL.INVALID_WINDOW,
  REFUSAL.INVALID_MAX_SEEN,
  REFUSAL.INVALID_MAX_PER_SENDER,
  REFUSAL.INVALID_PROMPT_TTL,
  REFUSAL.INVALID_FUTURE_TOLERANCE,
  REFUSAL.UNKNOWN_KIND,
  REFUSAL.INVALID_SEEN,
  REFUSAL.INVALID_SEEN_ENTRY,
  REFUSAL.DUPLICATE_SEEN_ENTRY,
  REFUSAL.UNSORTED_SEEN,
]);

/**
 * The replay window, in milliseconds: how old a message may be and still be a
 * delivery rather than a replay.
 *
 * Six hours is F19's own number, not a new one — the finding that a phone
 * "offline for six hours will re-deliver everything it holds" is the case this
 * window has to admit, and a window shorter than the offline gap would refuse
 * the very redeliveries at-least-once delivery exists to make. It is a bound
 * on *this* module's memory as much as on the protocol: an entry is
 * evictable once it is acked and older than the window, so the window and the
 * cap together bound what the ledger can hold.
 *
 * `MIN_WINDOW_MS` refuses a window short enough to refuse ordinary latency,
 * and `MAX_WINDOW_MS` refuses one long enough that the ledger's cap could
 * never be reached without the window's help — both are refusals rather than
 * clamps, so a typo cannot quietly change what the protocol admits.
 *
 * This window is the **command** window. By the owner's ruling of 2026-10-17
 * it applies to commands unchanged, and a prompt is judged by its own TTL
 * instead (`promptTtlMs`, below).
 */
export const DEFAULT_WINDOW_MS = 6 * 60 * 60 * 1_000;
export const MIN_WINDOW_MS = 1_000;
export const MAX_WINDOW_MS = 7 * 24 * 60 * 60 * 1_000;

/**
 * The prompt window: a prompt is accepted for as long as its TTL (D118's
 * mailbox TTL) and refused as `expired` after it. D118 states that there is a
 * TTL but no number, so this module invents none: `promptTtlMs` is a
 * **required** state field, handed in by the consumer (P4.1) from the TTL its
 * sweeper enforces, and validated `MIN_WINDOW_MS .. MAX_PROMPT_TTL_MS` — a
 * refusal, not a clamp. Thirty days is a ceiling on the ledger's memory, not
 * a retention policy.
 */
export const MAX_PROMPT_TTL_MS = 30 * 24 * 60 * 60 * 1_000;

/**
 * The dedupe ledger's cap: how many `(sender, id)` records a mailbox holds.
 *
 * 1024 is a bounded default an operator can move, not a measurement: D118
 * bounds one message, and this bounds how many the ledger remembers, so the
 * memory the mailbox's bookkeeping can take is `maxSeen` records of three
 * short fields rather than a function of how long the phone has been offline.
 * The cap is refused rather than grown when it is reached (`dedupe-full`).
 */
export const DEFAULT_MAX_SEEN = 1_024;
export const MIN_MAX_SEEN = 1;
export const MAX_MAX_SEEN = 100_000;

/**
 * The per-sender quota: how many of the ledger's `maxSeen` records one sender
 * may hold at once. Without it one sender could fill the whole ledger and
 * lock every other sender out with `dedupe-full` for up to a window — and for
 * ever, if the consumer stops acking, because un-acked records are never
 * evicted. A sender at its quota is refused (`sender-full`); nothing is
 * evicted to make room for it.
 *
 * The default is a quarter of the cap, at least one (`defaultMaxPerSender`):
 * four full senders can still fill the ledger, and *how many* senders may
 * write is P2.1's rules' business, not this module's. It is a state field,
 * validated `1 .. maxSeen`, so an operator can move it.
 */
export const SENDER_SHARE = 4;

/** The default per-sender quota for a cap: `max(1, floor(maxSeen / SENDER_SHARE))`. */
export function defaultMaxPerSender(maxSeen) {
  return Math.max(1, Math.floor(maxSeen / SENDER_SHARE));
}

/**
 * The latest time this module reads, on a `now` or an `at`: the lease's own
 * ceiling (`shared/lease`'s MAX_TIMESTAMP, 2100-01-01T00:00:00Z), imported
 * rather than restated so the two modules cannot drift. A `now` beyond it is
 * refused, so one huge `now` can push the mailbox's clock no further than the
 * ceiling — `now-went-backwards` is permanent only up to there.
 */
export const TIME_MAX = MAX_TIMESTAMP;

/**
 * How far into the future a message's `at` may be before it is refused.
 *
 * Five minutes covers ordinary clock skew and latency between the writer's
 * clock and the server's, and it is the lease's `MAX_CLOCK_SKEW`, imported
 * rather than restated. The bound is not decoration: a future `at` is
 * *inside* the window by definition (its age is negative), so an unbounded
 * future timestamp would be an entry that no window could ever evict — a
 * memory lever a writer could pull. Beyond the tolerance it is refused
 * (`future-timestamp`), and D117 is why the tolerance exists at all: the
 * server's timestamp is the authority, and a writer whose clock is far ahead
 * is a writer whose `at` cannot be trusted.
 */
export const DEFAULT_FUTURE_TOLERANCE_MS = MAX_CLOCK_SKEW;
export const MAX_FUTURE_TOLERANCE_MS = 24 * 60 * 60 * 1_000;

/**
 * The declared transition table: for every phase and every event, the outcomes
 * the cell can answer with, the refusals it can answer with, and the phases it
 * can leave the mailbox in.
 *
 * This is a *declaration*, and `index.test.js` asserts the code against it cell
 * by cell — a table that merely described whatever the code happened to do
 * would be a second copy of the code, and the point of writing it down is that
 * the two can disagree. Every state x every event has a cell: the machine is
 * total, and there is no event that leaves it undecided.
 */
export const TRANSITIONS = Object.freeze({
  [STATE.IDLE]: Object.freeze({
    [EVENT.DELIVER]: Object.freeze({
      ok: Object.freeze([OUTCOME.ACCEPTED, OUTCOME.DUPLICATE]),
      no: DELIVERY_REFUSALS(),
      next: Object.freeze([STATE.PENDING, STATE.IDLE]),
    }),
    [EVENT.ACT]: Object.freeze({
      ok: Object.freeze([OUTCOME.NOTHING_TO_ACT]),
      no: Object.freeze([]),
      next: Object.freeze([STATE.IDLE]),
    }),
    [EVENT.ACK]: Object.freeze({
      ok: Object.freeze([OUTCOME.NOTHING_TO_ACK]),
      no: Object.freeze([]),
      next: Object.freeze([STATE.IDLE]),
    }),
    [EVENT.CLOSE]: Object.freeze({
      ok: Object.freeze([OUTCOME.CLOSED]),
      no: Object.freeze([]),
      next: Object.freeze([STATE.CLOSED]),
    }),
  }),
  [STATE.PENDING]: Object.freeze({
    [EVENT.DELIVER]: Object.freeze({
      ok: Object.freeze([OUTCOME.ACCEPTED, OUTCOME.DUPLICATE]),
      no: DELIVERY_REFUSALS(),
      next: Object.freeze([STATE.PENDING]),
    }),
    [EVENT.ACT]: Object.freeze({
      // `stale-pending` when the oldest accepted record is outside the window.
      ok: Object.freeze([OUTCOME.ACTED, OUTCOME.STALE_PENDING]),
      no: Object.freeze([]),
      next: Object.freeze([STATE.ACTED, STATE.PENDING]),
    }),
    [EVENT.ACK]: Object.freeze({
      // A pending mailbox may also hold acted records (deliver a, act,
      // deliver b): the ack acks the oldest of those and the mailbox stays
      // pending, because b is still accepted.
      ok: Object.freeze([OUTCOME.ACKED, OUTCOME.NOTHING_TO_ACK]),
      no: Object.freeze([]),
      next: Object.freeze([STATE.PENDING]),
    }),
    [EVENT.CLOSE]: Object.freeze({
      ok: Object.freeze([OUTCOME.CLOSED]),
      no: Object.freeze([]),
      next: Object.freeze([STATE.CLOSED]),
    }),
  }),
  [STATE.ACTED]: Object.freeze({
    [EVENT.DELIVER]: Object.freeze({
      ok: Object.freeze([OUTCOME.ACCEPTED, OUTCOME.DUPLICATE]),
      no: DELIVERY_REFUSALS(),
      next: Object.freeze([STATE.PENDING, STATE.ACTED]),
    }),
    [EVENT.ACT]: Object.freeze({
      ok: Object.freeze([OUTCOME.NOTHING_TO_ACT]),
      no: Object.freeze([]),
      next: Object.freeze([STATE.ACTED]),
    }),
    [EVENT.ACK]: Object.freeze({
      ok: Object.freeze([OUTCOME.ACKED]),
      no: Object.freeze([]),
      next: Object.freeze([STATE.IDLE, STATE.ACTED]),
    }),
    [EVENT.CLOSE]: Object.freeze({
      ok: Object.freeze([OUTCOME.CLOSED]),
      no: Object.freeze([]),
      next: Object.freeze([STATE.CLOSED]),
    }),
  }),
  [STATE.CLOSED]: Object.freeze({
    [EVENT.DELIVER]: closedCell(),
    [EVENT.ACT]: closedCell(),
    [EVENT.ACK]: closedCell(),
    [EVENT.CLOSE]: closedCell(),
  }),
});

/**
 * The refusals a delivery can answer with: this module's own, plus every code
 * the schema can refuse a message with, because a delivery is refused by the
 * schema's verdict as much as by the window.
 */
function DELIVERY_REFUSALS() {
  return Object.freeze([
    ...new Set([
      REFUSAL.NOT_AN_OBJECT,
      REFUSAL.UNKNOWN_FIELD,
      REFUSAL.MISSING_FIELD,
      REFUSAL.NEGATIVE_ZERO,
      REFUSAL.ZERO_TIMESTAMP,
      REFUSAL.REPLAY,
      REFUSAL.EXPIRED,
      REFUSAL.FUTURE_TIMESTAMP,
      REFUSAL.ID_CONFLICT,
      REFUSAL.DEDUPE_FULL,
      REFUSAL.SENDER_FULL,
      ...Object.values(REFUSALS),
    ]),
  ]);
}

/** The one cell a closed mailbox has, for every event: refuse, stay closed. */
function closedCell() {
  return Object.freeze({
    ok: Object.freeze([]),
    no: Object.freeze([REFUSAL.MAILBOX_CLOSED]),
    next: Object.freeze([STATE.CLOSED]),
  });
}

/** The state's own keys. An unknown one is refused: a state is not a bag. */
const STATE_FIELDS = Object.freeze([
  'phase',
  'now',
  'windowMs',
  'maxSeen',
  'maxPerSender',
  'promptTtlMs',
  'futureToleranceMs',
  'seen',
]);

/**
 * An event's keys, exactly: `type`, the injected `now`, and a delivery's
 * message and (optional) kind.
 */
const EVENT_FIELDS = Object.freeze(['type', 'now', 'message', 'kind']);

/** A ledger record's keys, exactly. */
const SEEN_FIELDS = Object.freeze(['sender', 'id', 'at', 'kind', 'status']);

/** The kind vocabulary. */
const KIND_VALUES = Object.freeze(Object.values(KIND));

/** The event vocabulary, for the one check that is not a table lookup. */
const EVENT_VALUES = Object.freeze(Object.values(EVENT));

/** The status vocabulary, for the ledger's own validation. */
const STATUS_VALUES = Object.freeze(Object.values(STATUS));

/**
 * The dispatch table: phase -> event -> the handler. It is keyed by the same
 * two vocabularies `TRANSITIONS` declares, so a phase or an event that is not
 * in one is not in the other either.
 *
 * The three open rows share their handlers on purpose: within them the *ledger*
 * decides — a phase is a summary of the ledger and `readState` refuses a state
 * whose summary disagrees with it, so an open row cannot be reached with a
 * ledger that contradicts it. The closed row is the one row that behaves
 * differently for the same ledger, which is why the phase is load-bearing at
 * all.
 */
const DISPATCH = Object.freeze({
  [STATE.IDLE]: openRow(),
  [STATE.PENDING]: openRow(),
  [STATE.ACTED]: openRow(),
  [STATE.CLOSED]: closedRow(),
});

function openRow() {
  return Object.freeze({
    [EVENT.DELIVER]: deliver,
    [EVENT.ACT]: act,
    [EVENT.ACK]: ack,
    [EVENT.CLOSE]: closeMailbox,
  });
}

function closedRow() {
  return Object.freeze({
    [EVENT.DELIVER]: refuseClosed,
    [EVENT.ACT]: refuseClosed,
    [EVENT.ACK]: refuseClosed,
    [EVENT.CLOSE]: refuseClosed,
  });
}

/**
 * initialState builds the mailbox a consumer starts from: idle, an empty
 * ledger, and the five numbers the machine is judged with.
 *
 *     initialState({ now, promptTtlMs, windowMs?, maxSeen?, maxPerSender?, futureToleranceMs? })
 *
 * `now` is required and is the caller's — the server-anchored time (D117) the
 * first event will be judged against. `promptTtlMs` is required: it is D118's
 * mailbox TTL, which has no number this module could default to. The rest
 * default to the constants above (`maxPerSender` to
 * `defaultMaxPerSender(maxSeen)`). It returns a frozen `{ ok: true, state }`
 * or a refusal; it never throws.
 *
 * @param {{ now: number, promptTtlMs: number, windowMs?: number, maxSeen?: number, maxPerSender?: number, futureToleranceMs?: number }} [options]
 * @returns {{ ok: true, state: object } | { ok: false, code: string, field: string | null }}
 */
export function initialState(options = {}) {
  const read = readFields(
    options,
    STATE_FIELDS.filter((field) => field !== 'phase' && field !== 'seen'),
    'options',
  );
  if (!read.ok) return read;
  const values = read.values;

  const now = values.now;
  if (now === undefined) return refuse(REFUSAL.MISSING_FIELD, 'now');
  if (!isIntegerInRange(now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');

  const windowMs = values.windowMs === undefined ? DEFAULT_WINDOW_MS : values.windowMs;
  if (!isIntegerInRange(windowMs, MIN_WINDOW_MS, MAX_WINDOW_MS)) {
    return refuse(REFUSAL.INVALID_WINDOW, 'windowMs');
  }

  const maxSeen = values.maxSeen === undefined ? DEFAULT_MAX_SEEN : values.maxSeen;
  if (!isIntegerInRange(maxSeen, MIN_MAX_SEEN, MAX_MAX_SEEN)) {
    return refuse(REFUSAL.INVALID_MAX_SEEN, 'maxSeen');
  }

  const maxPerSender =
    values.maxPerSender === undefined ? defaultMaxPerSender(maxSeen) : values.maxPerSender;
  if (!isIntegerInRange(maxPerSender, 1, maxSeen)) {
    return refuse(REFUSAL.INVALID_MAX_PER_SENDER, 'maxPerSender');
  }

  const promptTtlMs = values.promptTtlMs;
  if (promptTtlMs === undefined) return refuse(REFUSAL.MISSING_FIELD, 'promptTtlMs');
  if (!isIntegerInRange(promptTtlMs, MIN_WINDOW_MS, MAX_PROMPT_TTL_MS)) {
    return refuse(REFUSAL.INVALID_PROMPT_TTL, 'promptTtlMs');
  }

  const futureToleranceMs =
    values.futureToleranceMs === undefined ? DEFAULT_FUTURE_TOLERANCE_MS : values.futureToleranceMs;
  if (!isIntegerInRange(futureToleranceMs, 0, MAX_FUTURE_TOLERANCE_MS)) {
    return refuse(REFUSAL.INVALID_FUTURE_TOLERANCE, 'futureToleranceMs');
  }

  return Object.freeze({
    ok: true,
    state: Object.freeze({
      phase: STATE.IDLE,
      now,
      windowMs,
      maxSeen,
      maxPerSender,
      promptTtlMs,
      futureToleranceMs,
      seen: Object.freeze([]),
    }),
  });
}

/**
 * step applies one event to one state.
 *
 *     step(state, { type: 'deliver', now, message })
 *     step(state, { type: 'act' | 'ack' | 'close', now })
 *
 * Every event carries the injected `now`; a delivery carries the message,
 * which is validated by the schema (P0.2) and whose codes are propagated
 * verbatim with a `message.` prefix. It returns `{ ok: true, outcome, phase,
 * id, sender, state }` — where `id`/`sender` name the delivery the outcome is
 * about, or are null when the outcome is about nothing — or a refusal
 * `{ ok: false, code, field }`. It never throws, never mutates its inputs and
 * never reads a clock.
 *
 * @param {object} state the mailbox, as returned by this function or initialState
 * @param {object} event the event, with its own injected `now`
 * @returns {{ ok: true, outcome: string, phase: string, id: string | null, sender: string | null, state: object }
 *          | { ok: false, code: string, field: string | null }}
 */
export function step(state, event) {
  const current = readState(state);
  if (!current.ok) return current;

  const read = readEvent(event);
  if (!read.ok) return read;

  const { type, now } = read.values;
  if (now < current.state.now) {
    // The window is a function of `now`, and the eviction rule depends on
    // "outside the window" being permanent. A `now` that goes backwards would
    // make an evicted entry's message look deliverable again, so it is refused
    // rather than absorbed.
    return refuse(REFUSAL.NOW_WENT_BACKWARDS, 'now');
  }

  const row = DISPATCH[current.state.phase];
  if (typeof type !== 'string' || !Object.hasOwn(row, type)) {
    return refuse(REFUSAL.UNKNOWN_EVENT, 'type');
  }
  return row[type](current.state, read.values);
}

// ---------------------------------------------------------------------------
// The handlers
// ---------------------------------------------------------------------------

/**
 * deliver: a message has arrived, possibly again.
 *
 * The order of the checks is the rule. The schema first — a value that is not
 * a message is not a delivery. Then the message's own time: negative zero, which
 * the schema accepts (it is numerically zero), and zero itself, which is a
 * schema-valid `at` this module will not carry, both refused here because they
 * are the shape of a negated or unset counter rather than a time. Then the
 * future bound, before the window: a far-future `at` would otherwise compute a
 * negative age and read as "inside the window". Then the window, which is the
 * replay rule. Then the ledger: the same `(sender, id)` — whatever the age of
 * its record — is a duplicate when its `at` agrees and a conflict when it does
 * not. Then the cap, and then the sender's quota.
 */
function deliver(state, values) {
  const validated = validateMessage(values.message);
  if (!validated.ok) {
    // The schema names an unknown field by its key, which the writer chose:
    // only a field the schema itself defines is echoed, anything else is
    // reported as the message as a whole.
    return refuse(
      validated.code,
      MESSAGE_FIELDS.includes(validated.field) ? `message.${validated.field}` : 'message',
    );
  }

  const { sender, id, at } = validated.message;
  if (Object.is(at, -0)) return refuse(REFUSAL.NEGATIVE_ZERO, 'message.at');
  // `at: 0` is schema-valid (P0.2 allows any safe integer >= 0) and is not a
  // time this module will carry: the ledger's own floor, the state's `now`
  // floor and the presence rule (D117: a zero timestamp is the shape of an
  // unset counter) all say a time is a *positive* integer. Admitting it here
  // would be worse than refusing it -- the delivery would be accepted, written
  // into the ledger, and then refused by this module's own reader
  // (`invalid-seen-entry`), leaving a message nobody could act on, ack or
  // close. The guard is written `at < 1` rather than `at === 0` so that it
  // stays right if the schema's floor ever moves.
  if (at < 1) return refuse(REFUSAL.ZERO_TIMESTAMP, 'message.at');
  // The ceiling is checked on its own, not left to the tolerance: with `now`
  // at the ceiling an `at` inside the tolerance could pass it, be written into
  // the ledger and then be refused by this module's own reader.
  if (at > TIME_MAX) return refuse(REFUSAL.FUTURE_TIMESTAMP, 'message.at');
  if (at - values.now > state.futureToleranceMs) {
    return refuse(REFUSAL.FUTURE_TIMESTAMP, 'message.at');
  }
  // The kind's own window: a command older than `windowMs` is a replay, a
  // prompt older than its TTL has expired. Two codes, because "replayed" and
  // "waited too long" are different facts for the consumer to report.
  const kind = values.kind === undefined ? KIND.COMMAND : values.kind;
  if (values.now - at > windowFor(state, kind)) {
    return refuse(kind === KIND.PROMPT ? REFUSAL.EXPIRED : REFUSAL.REPLAY, 'message.at');
  }

  // A record is consulted whatever its age. A pair the ledger still holds is
  // never a new delivery: un-acked records are never evicted, so a stale
  // record for this pair is either work in flight (acting on the "new" one
  // would be the same id acted on twice) or an acked record not yet evicted.
  // The kind is part of the message's identity: the same pair and `at` under
  // another kind is a second message, not a duplicate.
  const known = state.seen.find((entry) => entry.sender === sender && entry.id === id);
  if (known !== undefined) {
    if (known.at !== at || known.kind !== kind) return refuse(REFUSAL.ID_CONFLICT, 'message.at');
    // A duplicate is answered, not acted on, and the ledger is left exactly as
    // it was: a redelivery must not re-arm a delivery that was already acted
    // on, and must not resurrect an acked one.
    return answered(OUTCOME.DUPLICATE, state.phase, state, values.now, state.seen, sender, id);
  }

  const kept = evict(state, values.now);
  if (kept.length >= state.maxSeen) {
    // Nothing evictable and no room: refusing is the only answer that does not
    // lose a record, and a lost record is a message that can be acted on twice.
    return refuse(REFUSAL.DEDUPE_FULL, 'seen');
  }
  if (kept.filter((entry) => entry.sender === sender).length >= state.maxPerSender) {
    // One sender may not take the ledger from every other one. Refused, never
    // evicted: the quota is a fence, not a reason to forget.
    return refuse(REFUSAL.SENDER_FULL, 'message.sender');
  }
  const entries = insert(kept, Object.freeze({ sender, id, at, kind, status: STATUS.ACCEPTED }));
  return answered(OUTCOME.ACCEPTED, STATE.PENDING, state, values.now, entries, sender, id);
}

/**
 * act: do the thing the oldest un-acted delivery asked for.
 *
 * The oldest by the ledger's canonical order, so two deliveries in either
 * order are acted on in the same sequence — "at-least-once" says nothing about
 * the order a mailbox learns of them, and an order that depended on arrival
 * would make the state a function of the network.
 *
 * A record the window has already passed is still acted on — exactly once,
 * like any other — and the outcome is `stale-pending` instead of `acted`, so
 * the consumer learns the work is late rather than never learning of it. What
 * late means (perform it anyway, or tell the sender it expired) is the
 * consumer's to decide; this module never drops it.
 */
function act(state, values) {
  return moveOldest(state, values, STATUS.ACCEPTED, STATUS.ACTED, (target) =>
    values.now - target.at > windowFor(state, target.kind) ? OUTCOME.STALE_PENDING : OUTCOME.ACTED,
  OUTCOME.NOTHING_TO_ACT);
}

/** ack: tell the sender the oldest acted delivery is done (D110's ack). */
function ack(state, values) {
  return moveOldest(state, values, STATUS.ACTED, STATUS.ACKED, () => OUTCOME.ACKED, OUTCOME.NOTHING_TO_ACK);
}

function moveOldest(state, values, from, to, outcomeFor, nothing) {
  const index = state.seen.findIndex((entry) => entry.status === from);
  if (index === -1) {
    // Not a refusal: "there was nothing to act on" is a fact about the mailbox,
    // and a consumer that re-acts on a redelivery is asking a question whose
    // answer is this.
    return answered(nothing, state.phase, state, values.now, state.seen, null, null);
  }
  const target = state.seen[index];
  const entries = state.seen.map((entry, position) =>
    position === index
      ? Object.freeze({ sender: entry.sender, id: entry.id, at: entry.at, kind: entry.kind, status: to })
      : entry,
  );
  return answered(outcomeFor(target), derivePhase(entries), state, values.now, entries, target.sender, target.id);
}

/**
 * close: the mailbox is done. The ledger is kept — what was seen is still what
 * was seen — and every later event is refused (`mailbox-closed`), so a closed
 * mailbox cannot be reopened by a redelivery that arrives late.
 */
function closeMailbox(state, values) {
  return answered(OUTCOME.CLOSED, STATE.CLOSED, state, values.now, state.seen, null, null);
}

/** Every event on a closed mailbox, and the one code they all answer with. */
function refuseClosed() {
  return refuse(REFUSAL.MAILBOX_CLOSED, 'phase');
}

// ---------------------------------------------------------------------------
// The ledger
// ---------------------------------------------------------------------------

/**
 * evict drops the records that may be dropped, and returns the survivors as a
 * new array. A record is dropped only when it is **acked** — the delivery is
 * finished, so forgetting it cannot lose work — AND **strictly** outside its
 * kind's window (its age is greater than `windowMs` for a command, or
 * `promptTtlMs` for a prompt), so that any redelivery of its message is
 * already refused (`replay`, `expired`) before the ledger is read. At an age
 * of exactly the window the message is still deliverable, so its record must
 * still be there to call the redelivery a duplicate. A prompt record therefore
 * outlives a command record of the same age: it is never evicted before its
 * TTL passes, and never re-admitted after.
 *
 * Nothing that is not acked is ever dropped, at any age: an accepted record is
 * work not yet done, and an acted one is work this consumer still owes an ack
 * for. Dropping either would lose the work or make room for a second action.
 */
function evict(state, now) {
  return state.seen.filter((entry) => {
    if (entry.status !== STATUS.ACKED) return true;
    return now - entry.at <= windowFor(state, entry.kind);
  });
}

/** The acceptance window of a kind: the command window, or the prompt TTL. */
function windowFor(state, kind) {
  return kind === KIND.PROMPT ? state.promptTtlMs : state.windowMs;
}

/** insert keeps the ledger in its canonical order, without a sort. */
function insert(entries, entry) {
  let index = 0;
  while (index < entries.length && compareEntries(entries[index], entry) < 0) index += 1;
  return [...entries.slice(0, index), entry, ...entries.slice(index)];
}

/**
 * The canonical order: `at`, then `sender`, then `id`, compared by code unit
 * and never by locale. `at` first because it is the order the mailbox would
 * have learned the messages in had the network been kind; `sender` and `id`
 * only to make the order total, so that two messages with the same `at` cannot
 * leave two different states depending on which arrived first.
 */
function compareEntries(left, right) {
  if (left.at !== right.at) return left.at < right.at ? -1 : 1;
  if (left.sender !== right.sender) return left.sender < right.sender ? -1 : 1;
  if (left.id !== right.id) return left.id < right.id ? -1 : 1;
  return 0;
}

/** The ledger's key for a delivery: `(sender, id)`, and not the id alone. */
function pairKey(sender, id) {
  return `${sender.length}:${sender}${id}`;
}

/** The phase the ledger says the mailbox is in. */
function derivePhase(entries) {
  if (entries.some((entry) => entry.status === STATUS.ACCEPTED)) return STATE.PENDING;
  if (entries.some((entry) => entry.status === STATUS.ACTED)) return STATE.ACTED;
  return STATE.IDLE;
}

// ---------------------------------------------------------------------------
// Reading the inputs
// ---------------------------------------------------------------------------

/**
 * readState reads a state into a frozen snapshot, or refuses.
 *
 * Every field is read exactly once, through its own property descriptor, so a
 * getter cannot answer one thing to the check and another to the use; a value
 * with a prototype of its own is not a state; an unknown field is refused; and
 * the declared phase must agree with the ledger.
 */
function readState(state) {
  const read = readFields(state, STATE_FIELDS, 'state');
  if (!read.ok) return read;
  const values = read.values;

  for (const field of STATE_FIELDS) {
    if (values[field] === undefined) return refuse(REFUSAL.MISSING_FIELD, field);
  }

  if (!Object.values(STATE).includes(values.phase)) return refuse(REFUSAL.UNKNOWN_STATE, 'phase');

  if (!isIntegerInRange(values.now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');
  if (!isIntegerInRange(values.windowMs, MIN_WINDOW_MS, MAX_WINDOW_MS)) {
    return refuse(REFUSAL.INVALID_WINDOW, 'windowMs');
  }
  if (!isIntegerInRange(values.maxSeen, MIN_MAX_SEEN, MAX_MAX_SEEN)) {
    return refuse(REFUSAL.INVALID_MAX_SEEN, 'maxSeen');
  }
  if (!isIntegerInRange(values.maxPerSender, 1, values.maxSeen)) {
    return refuse(REFUSAL.INVALID_MAX_PER_SENDER, 'maxPerSender');
  }
  if (!isIntegerInRange(values.promptTtlMs, MIN_WINDOW_MS, MAX_PROMPT_TTL_MS)) {
    return refuse(REFUSAL.INVALID_PROMPT_TTL, 'promptTtlMs');
  }
  if (!isIntegerInRange(values.futureToleranceMs, 0, MAX_FUTURE_TOLERANCE_MS)) {
    return refuse(REFUSAL.INVALID_FUTURE_TOLERANCE, 'futureToleranceMs');
  }

  const seen = readSeen(values.seen, values.maxSeen);
  if (!seen.ok) return seen;

  if (values.phase !== STATE.CLOSED && values.phase !== derivePhase(seen.entries)) {
    // The phase is a summary of the ledger. A state whose summary disagrees
    // with what it holds has two answers to every question, and this module
    // will not pick one and call the other a bug later.
    return refuse(REFUSAL.INVALID_STATE, 'phase');
  }

  return {
    ok: true,
    state: Object.freeze({
      phase: values.phase,
      now: values.now,
      windowMs: values.windowMs,
      maxSeen: values.maxSeen,
      maxPerSender: values.maxPerSender,
      promptTtlMs: values.promptTtlMs,
      futureToleranceMs: values.futureToleranceMs,
      seen: seen.entries,
    }),
  };
}

/**
 * readSeen reads the ledger: an array of `{ sender, id, at, kind, status }`
 * records, no longer than the state's own cap, one per `(sender, id)`, in
 * canonical order. Anything else is refused — a ledger that is not in order
 * cannot be searched, and one with two records for a pair cannot say whether a
 * redelivery is a duplicate.
 */
function readSeen(value, maxSeen) {
  const items = readArray(value, maxSeen);
  if (items === null) return refuse(REFUSAL.INVALID_SEEN, 'seen');

  const entries = [];
  for (const raw of items) {
    const read = readFields(raw, SEEN_FIELDS, 'seen');
    if (!read.ok) return refuse(REFUSAL.INVALID_SEEN_ENTRY, 'seen');
    const entry = read.values;
    if (
      typeof entry.sender !== 'string' ||
      entry.sender === '' ||
      entry.sender.length > SENDER_MAX ||
      typeof entry.id !== 'string' ||
      entry.id === '' ||
      entry.id.length > ID_MAX ||
      !isIntegerInRange(entry.at, 1, TIME_MAX) ||
      !KIND_VALUES.includes(entry.kind) ||
      !STATUS_VALUES.includes(entry.status)
    ) {
      return refuse(REFUSAL.INVALID_SEEN_ENTRY, 'seen');
    }
    entries.push(
      Object.freeze({
        sender: entry.sender,
        id: entry.id,
        at: entry.at,
        kind: entry.kind,
        status: entry.status,
      }),
    );
  }

  const keys = new Set();
  for (const entry of entries) {
    const key = pairKey(entry.sender, entry.id);
    if (keys.has(key)) return refuse(REFUSAL.DUPLICATE_SEEN_ENTRY, 'seen');
    keys.add(key);
  }
  for (let index = 1; index < entries.length; index += 1) {
    if (compareEntries(entries[index - 1], entries[index]) >= 0) {
      return refuse(REFUSAL.UNSORTED_SEEN, 'seen');
    }
  }

  return { ok: true, entries: Object.freeze(entries) };
}

/**
 * readEvent reads an event: `type` and the injected `now`, plus the message a
 * delivery carries and only a delivery. The type is *not* checked against the
 * event vocabulary here — that is the dispatch table's one guard, and a second
 * copy of it would be a second answer.
 */
function readEvent(event) {
  const read = readFields(event, EVENT_FIELDS, 'event');
  if (!read.ok) return read;
  const values = read.values;

  if (values.type === undefined) return refuse(REFUSAL.MISSING_FIELD, 'type');
  if (values.now === undefined) return refuse(REFUSAL.MISSING_FIELD, 'now');
  // The injected now is a time like any other, and is read like one: a string,
  // a float, a negative zero or a value past the exact-double line is refused
  // here rather than compared against the state's own now and reported as an
  // ordering problem it is not.
  if (!isIntegerInRange(values.now, 1, TIME_MAX)) {
    return refuse(REFUSAL.INVALID_NOW, 'now');
  }

  // The kind is a delivery's, and only a delivery's; absent, it is a command.
  if (values.kind !== undefined) {
    if (values.type !== EVENT.DELIVER && EVENT_VALUES.includes(values.type)) {
      return refuse(REFUSAL.UNKNOWN_FIELD, 'event');
    }
    if (!KIND_VALUES.includes(values.kind)) return refuse(REFUSAL.UNKNOWN_KIND, 'kind');
  }

  const hasMessage = values.message !== undefined;
  if (values.type === EVENT.DELIVER && !hasMessage) {
    return refuse(REFUSAL.MISSING_FIELD, 'message');
  }
  if (values.type !== EVENT.DELIVER && hasMessage && EVENT_VALUES.includes(values.type)) {
    return refuse(REFUSAL.UNKNOWN_FIELD, 'event');
  }

  return { ok: true, values };
}

/**
 * readFields reads exactly the named fields into a null-prototype snapshot,
 * each field once, through its own descriptor.
 *
 * A value that is not a plain data object is refused (`not-an-object`), a key
 * the caller does not name — a symbol included — is refused (`unknown-field`):
 * the field set is the version, and a state or an event from a later protocol
 * must be refused rather than silently read with the new field dropped. An
 * accessor property is refused (`accessor-property`) without ever being
 * called, because a getter can answer differently each time it is read, and
 * this module's whole claim is that the answer is a function of the value it
 * was handed.
 *
 * An unknown key is reported by `where` — the container this module named —
 * and never by the key itself: the key is the input's choice, and a refusal
 * that echoed it would log whatever a writer chose to put there. An accessor
 * is reported by its key, which by then is one of `names`.
 */
function readFields(value, names, where) {
  if (!isPlainObject(value)) return refuse(REFUSAL.NOT_AN_OBJECT, where);

  const values = Object.create(null);
  for (const key of Reflect.ownKeys(value)) {
    // A symbol is never one of `names`, so it is refused here too.
    if (!names.includes(key)) return refuse(REFUSAL.UNKNOWN_FIELD, where);
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (descriptor === undefined || !Object.hasOwn(descriptor, 'value')) {
      return refuse(REFUSAL.ACCESSOR_PROPERTY, key);
    }
    values[key] = descriptor.value;
  }
  return { ok: true, values };
}

/**
 * readArray reads a ledger array into a fresh plain array, or answers null.
 *
 * It is read by index, never iterated: an own `Symbol.iterator`, or a tampered
 * `Array.prototype[Symbol.iterator]`, would otherwise decide which records the
 * module sees — a pending record could be skipped and lost, or the cap read off
 * a length the iteration never honoured. So the array must be a plain one
 * (`Array.prototype`, not a subclass), its length is read once and checked
 * against the cap before anything else, its own keys must be exactly the
 * indices and `length` (no holes, no extra properties, no symbols), and every
 * index must be a data property — a getter is never called. A Proxy can still
 * answer all of these dishonestly; see the README's honest limits.
 */
function readArray(value, max) {
  if (!Array.isArray(value) || Object.getPrototypeOf(value) !== Array.prototype) return null;
  const length = Object.getOwnPropertyDescriptor(value, 'length').value;
  if (length > max) return null;
  if (Reflect.ownKeys(value).length !== length + 1) return null;
  const items = [];
  for (let index = 0; index < length; index += 1) {
    const descriptor = Object.getOwnPropertyDescriptor(value, String(index));
    if (descriptor === undefined || !Object.hasOwn(descriptor, 'value')) return null;
    items.push(descriptor.value);
  }
  return items;
}

/**
 * A plain data object — an object literal, or one with no prototype — and not
 * an instance of anything. The check is here rather than implied by the field
 * checks because an instance can answer a field through its prototype chain or
 * a getter, which is a value this module cannot read once and be sure of.
 */
function isPlainObject(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

/**
 * An integer inside a closed range, and never a negative zero.
 *
 * `-0` passes every numeric check — it is zero, it is finite, it is an
 * integer — and is refused anyway: it is the shape of a number that has been
 * negated, which is a computation's residue rather than a time or a count.
 * `Object.is` is what tells the two apart, and this is the one place that
 * matters.
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

// ---------------------------------------------------------------------------
// The answers
// ---------------------------------------------------------------------------

/** A refusal: no state, a code the caller can branch on, and the field at fault. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/** A step: the outcome, the phase it left the mailbox in, and the new state. */
function answered(outcome, phase, state, now, entries, sender, id) {
  return Object.freeze({
    ok: true,
    outcome,
    phase,
    id,
    sender,
    state: Object.freeze({
      phase,
      now,
      windowMs: state.windowMs,
      maxSeen: state.maxSeen,
      maxPerSender: state.maxPerSender,
      promptTtlMs: state.promptTtlMs,
      futureToleranceMs: state.futureToleranceMs,
      seen: Object.freeze(entries),
    }),
  });
}
