// The spoken-confirmation state machine (mobile plan, Task P6.2).
//
// One pure function of a state and an event:
//
//     step(state, event) -> { ok: true, outcome, phase, ... , state }
//                        |  { ok: false, code, field }
//
// Why it exists (D128 as amended 2026-10-17, D169, D162): the owner ruled that
// a destructive or changing voice command -- cancel a job, approve/merge, and
// confirming an action -- is allowed in v1 **only with a spoken confirmation**:
// a read-back of exactly what will happen, then an explicit "confirm". Voice
// D47's Rule is what that confirmation must satisfy, and the ruling is read as
// satisfying it: the read-back names the exact run id rather than a paraphrase,
// the confirming utterance is a fixed, short, low-confusability phrase rather
// than "yes", and it is recognised as a separate utterance. This module is that
// ruling as a decision procedure: given what has been read back and one
// utterance, what happens next, and what is refused.
//
// D162 (answered 2026-10-17, against the recommendation) is the other half of
// the shape: a launch ("kick off ...") needs **no** confirmation. It passes
// through this machine without a read-back and without a record; the project's
// spend caps are what bound it.
//
// **The three phases, and what each one says.**
//
//     idle      no read-back is outstanding
//     pending   exactly one read-back is outstanding, and may be confirmed
//     crowded   two or more are; a confirm is ambiguous and is refused
//
// A phase is a **summary of the pending read-backs**, not a counter: `step`
// refuses a state whose declared phase disagrees with them (`invalid-state`),
// because a state that can be read two ways has two answers to every question.
// `crowded` exists so that "a confirm for a *different* target -- the pending
// action changed, or a second pending action -- is refused" (P6.2's own
// verification list) is a property of the state rather than a guess about which
// read-back the speaker meant.
//
// **The rules that make a confirmation mean one thing.**
//
//   * a destructive command enters `pending` **only with a resolved target**;
//     an unresolved or ambiguous target is refused (`unresolved-target`) and
//     therefore cannot be read back, so it can never be confirmed;
//   * the **read-back text is built from the resolved target and from nothing
//     else** -- never from the transcript (P6.2: "the read-back for 'cancel the
//     docs refactor' names the resolved run id"). It is recomputed, never
//     stored, so a state handed back with an edited read-back cannot be spoken;
//   * a confirmation is a **separate utterance** (`same-utterance` refuses the
//     utterance that issued the command) recognised as the **fixed phrase**
//     (`not-a-confirmation` refuses "conform", "confirmed it", "yes", "sure");
//   * an utterance that carries a command **and** a confirm token is refused
//     (`command-and-confirm`): the same misrecognition must not satisfy both
//     halves of D47's Rule;
//   * **one confirmation authorises exactly one action**: it carries the action
//     id, the pending is consumed, and every id it spends (utterance, action,
//     confirmation) is recorded in a ledger, so a replay authorises nothing;
//   * it has a **timeout**, measured on the injected clock from the read-back:
//     at `at + timeoutMs` the confirmation is still inside, one millisecond
//     later it is refused (`confirmation-expired`);
//   * every other destructive verb -- the kill switch, budget writes, deletes
//     -- is refused outright (`refused-verb`), and so is a verb this module does
//     not know (`unknown-verb`): the verb set is closed, and the field set is
//     the version.
//
// **Time is injected.** Every event carries the `now` it is to be judged at,
// and the machine reads no clock of its own: the pending time is phone-local
// state (functional-spec-reconciliation.md 3.5), so the phone's clock is the one
// that decides the timeout, while the consumer's window (6 hours from the
// confirmation time) is P6.7's. A `now` earlier than the state's own is refused
// (`now-went-backwards`) and one more than MAX_FORWARD_JUMP_MS ahead is refused
// (`now-jumped`), so a single far-future `now` cannot freeze the machine.
//
// **Fail closed, and every refusal is named.** An unknown field, a wrong type,
// an accessor property, an id that is not a database key, a target of the wrong
// kind and a verb outside the closed set are each a REFUSAL with a code of its
// own, never a coerced value and never a guess. A refusal names the *field* at
// fault by a fixed path this module chose -- never the value it refused, and
// never a key name the input chose -- so a refusal can be logged. A refusal
// carries **no state**: the caller keeps what it had, which is why a refused
// utterance spends nothing.
//
// Nothing here reads a clock, a file, the network, a random source or a
// credential, and nothing here mutates what it is handed. It is the app's
// decision procedure and nothing else: the router that recognises the wake word
// and the phrase is P6.1's, the action record and its MAC are P6.3's, the
// prompts are P6.5's, and the consumer that re-validates the record and runs the
// command is P6.7's. `shared/README.md` is the package's contract and
// `../../docs/mobile/implementation-plan.md`'s P6.2 is this module's task.

import { CONFIRMATION_PHRASES, isConfirmation, routeUtterance } from '../command/index.js';
import { MAX_TIMESTAMP } from '../lease/index.js';
import { TARGET_MAX } from '../mailbox-schema/index.js';

/**
 * The three phases. Nothing else is a phase: a state carrying an unlisted one is
 * refused with `unknown-state` rather than treated as the nearest match.
 */
export const PHASE = Object.freeze({
  IDLE: 'idle',
  PENDING: 'pending',
  CROWDED: 'crowded',
});

/** The three events. An event whose `type` is not one of these is refused. */
export const EVENT = Object.freeze({
  COMMAND: 'command',
  CONFIRM: 'confirm',
  ABORT: 'abort',
});

/** What a step answers with when it does not refuse. */
export const OUTCOME = Object.freeze({
  /** A destructive command with a resolved target: the read-back to speak. */
  READ_BACK: 'read-back',
  /** A launch: D162, straight through, no read-back and no record. */
  PASS_THROUGH: 'pass-through',
  /** A confirmation that matched exactly one pending action: it is authorised. */
  CONFIRMED: 'confirmed',
  /** "cancel that": the outstanding read-backs were dropped. */
  DISCARDED: 'discarded',
  /** "cancel that" with nothing outstanding -- a fact, not a refusal. */
  NOTHING_PENDING: 'nothing-pending',
});

/** The kinds of resolved target, and the verbs each one belongs to. */
export const TARGET_KIND = Object.freeze({
  /** A run id: what a cancel cancels, and one thing a confirm may confirm. */
  RUN: 'run',
  /** A repository, a PR and the head read back: what a merge merges. */
  MERGE: 'merge',
  /** A repository and a PR with no head: the other thing a confirm may confirm (P6.3 refuses a head SHA on a confirm). */
  PR: 'pr',
});

/**
 * The verbs.
 *
 * `launch` is here even though it is not confirmed, because D162's answer is
 * about this machine: "kick off ..." is recognised as a launch and passes
 * through, rather than being refused as an unknown verb or silently treated as
 * destructive.
 */
export const VERB = Object.freeze({
  CANCEL: 'cancel',
  /** Confirming an action: spelled as P6.3's action record spells it. */
  CONFIRM_ACTION: 'confirm',
  MERGE: 'merge',
  LAUNCH: 'launch',
});

/**
 * The destructive verbs the owner's ruling of 2026-10-17 names: cancel a job,
 * approve/merge, and confirming an action. A command with one of these enters
 * `pending` and needs a confirmation.
 */
export const DESTRUCTIVE_VERBS = Object.freeze(['cancel', 'confirm', 'merge']);

/** The verbs that go straight through: D162's launch, and nothing else. */
export const PASS_THROUGH_VERBS = Object.freeze(['launch']);

/**
 * Every other destructive verb, refused outright by the ruling: the kill
 * switch, the budget writes, deletes (and the spellings a router may hand in --
 * `fugaro pair revoke` is a delete of a device node). The set is closed on
 * purpose: a verb that is neither destructive nor a launch is refused as
 * `unknown-verb`, so a new destructive verb is not silently unprotected.
 */
export const REFUSED_VERBS = Object.freeze([
  'budget',
  'budget-kill',
  'budget-resume',
  'budget-set',
  'delete',
  'delete-account',
  'destroy',
  'kill',
  'kill-switch',
  'purge',
  'reset',
  'revoke',
]);

/**
 * The fixed phrase (D128 as amended; Voice D47's "fixed, short,
 * low-confusability phrase"). It is **P6.1's** set of one, imported rather
 * than restated, so the router and this module cannot disagree about what a
 * confirmation is. "yes", "sure", "ok", "yeah", "confirmed" and "confirmed
 * it" are deliberately **not** in it (G10: the spec's own example "yes, merge
 * it" is not an acceptable confirmation).
 */
export const CONFIRM_PHRASES = CONFIRMATION_PHRASES;

/**
 * The cue appended to every read-back, so no consumer invents its own wording.
 * It names the wake word because P6.1 recognises "confirm" only as an app
 * command: a bare "confirm" is agent text (D164) and confirms nothing.
 */
export const CONFIRM_CUE = 'Say "Fugaro, confirm" to confirm.';

/**
 * The confirmation timeout (P6.2: "it has a timeout"). Nothing in the documents
 * states a number, so this is this module's own bound rather than a recorded
 * decision: a minute is long enough to hear a read-back and answer it, and short
 * enough that the target named in the read-back is still the target at the time
 * of the confirmation. It is a **state field the caller may move** inside
 * MIN..MAX; functional-spec-reconciliation.md 3.5 places this timeout on the
 * phone, where the pending state is.
 */
export const DEFAULT_CONFIRM_TIMEOUT_MS = 60 * 1000;

/** The floor and ceiling on the timeout: below it a read-back cannot be answered, above it, it is not a confirmation. */
export const MIN_CONFIRM_TIMEOUT_MS = 1 * 1000;
export const MAX_CONFIRM_TIMEOUT_MS = 5 * 60 * 1000;

/** How many read-backs may be outstanding at once: enough for a correction, not a queue. */
export const DEFAULT_MAX_PENDING = 4;
export const MIN_MAX_PENDING = 1;
export const MAX_MAX_PENDING = 16;

/**
 * How many spent ids the ledger remembers. It is a bound rather than a policy:
 * a full ledger REFUSES (`ledger-full`) instead of forgetting, because
 * forgetting re-opens a replay, and the caller can raise the cap. The
 * horizon-bearing ledger the consumer keeps is P6.7's, not this one's.
 */
export const DEFAULT_MAX_LEDGER = 1024;
export const MIN_MAX_LEDGER = 1;
export const MAX_MAX_LEDGER = 100000;

/** The permitted length of an utterance, action or confirmation id, in UTF-16 code units (P0.2's ID_MAX). */
export const ID_MAX = 100;

/** The permitted length of an utterance, in UTF-16 code units (P0.2's TEXT_MAX). */
export const TEXT_MAX = 4096;

/**
 * The permitted length of a repository, in UTF-16 code units: P6.3's
 * TARGET_MAX, imported, because a target this module reads back must be one
 * P6.3's action record can carry. Each segment is bounded too.
 */
export const REPO_MAX = TARGET_MAX;
export const REPO_SEGMENT_MAX = 100;

/**
 * P6.3's run-id and repository alphabets and shapes, restated (P6.3 does not
 * export them) and held to P6.3's validator by a test that wraps every record
 * this module authorises in a message and validates it. A run id is passed to
 * `fugaro cancel` and a repository to `gh pr merge --repo` as ARGV elements,
 * so a leading `-` or `.` would be a flag or a path to the consumer.
 */
const RUN_CHARS = /^[A-Za-z0-9._:-]+$/;
const RUN_PATTERN = /^[A-Za-z0-9_:][A-Za-z0-9._:-]*$/;
const REPO_SEGMENT_PATTERN = /^[A-Za-z0-9_][A-Za-z0-9._-]*$/;

/** The largest pull-request number this module will carry: a bound, not a measurement. */
export const PR_MAX = 10000000;

/** The latest `now` or `at` this module reads (shared/lease's MAX_TIMESTAMP, imported: two modules must not disagree about a time). */
export const TIME_MAX = MAX_TIMESTAMP;

/** How far one event's `now` may lead the state's; beyond it `now-jumped` (shared/mailbox's number, the same reasoning). */
export const MAX_FORWARD_JUMP_MS = 366 * 24 * 60 * 60 * 1000;

/** Characters a Realtime Database key may not contain (P0.2's rule, restated here because this module validates its own ids). */
const FORBIDDEN_KEY_CHARS = new Set(['%', '.', '$', '#', '[', ']', '/']);

/** The exact fields of a command event; any other field is refused. */
export const COMMAND_FIELDS = Object.freeze([
  'type',
  'now',
  'utteranceId',
  'text',
  'verb',
  'target',
  'actionId',
]);

/** The exact fields of a confirm event; any other field is refused. */
export const CONFIRM_FIELDS = Object.freeze([
  'type',
  'now',
  'utteranceId',
  'text',
  'actionId',
  'confirmationId',
  'target',
]);

/** The exact fields of an abort event; any other field is refused. */
export const ABORT_FIELDS = Object.freeze(['type', 'now', 'utteranceId', 'text']);

/** The exact fields of a state; any other field is refused. */
export const STATE_FIELDS = Object.freeze([
  'phase',
  'now',
  'timeoutMs',
  'maxPending',
  'maxLedger',
  'pending',
  'ledger',
]);

/** The exact fields of a pending read-back record. */
export const PENDING_FIELDS = Object.freeze(['actionId', 'verb', 'target', 'at', 'deadline', 'by']);

/** The exact fields of a ledger record. */
export const LEDGER_FIELDS = Object.freeze(['kind', 'id', 'at']);

/** The kinds of id the ledger remembers. */
export const LEDGER_KIND = Object.freeze({
  ACTION: 'action',
  CONFIRMATION: 'confirmation',
  UTTERANCE: 'utterance',
});

const LEDGER_KINDS = Object.freeze(['action', 'confirmation', 'utterance']);
const PHASE_VALUES = Object.freeze(Object.values(PHASE));

/** The field refusals every event can answer with: an id, a text, or a missing field. */
const ID_TEXT_REFUSALS = Object.freeze([
  'bad-character',
  'bad-key',
  'empty-string',
  'missing-field',
  'too-long',
  'unknown-field',
  'wrong-type',
]);

/** The refusals a command can answer with in every phase. */
const COMMAND_REFUSALS = Object.freeze([
  ...ID_TEXT_REFUSALS,
  'bad-head-sha',
  'bad-pr',
  'bad-repo',
  'command-and-confirm',
  'ledger-full',
  'refused-verb',
  'replayed-action',
  'replayed-utterance',
  'unknown-verb',
  'unresolved-target',
  'verb-target-mismatch',
]);

/** The extras only a phase that can still take another read-back adds. */
const COMMAND_FULL_REFUSALS = Object.freeze([...COMMAND_REFUSALS, 'pending-full']);

/** The refusals an abort can answer with: it carries no verb and no target. */
const ABORT_REFUSALS = Object.freeze([
  ...ID_TEXT_REFUSALS,
  'command-and-confirm',
  'ledger-full',
  'replayed-utterance',
]);

/** The refusals a confirm can answer with when nothing is outstanding. */
const IDLE_CONFIRM_REFUSALS = Object.freeze([
  ...ID_TEXT_REFUSALS,
  'no-pending-action',
  'not-a-confirmation',
]);

/** The refusals a confirm can answer with when exactly one read-back is outstanding. */
const PENDING_CONFIRM_REFUSALS = Object.freeze([
  ...ID_TEXT_REFUSALS,
  'bad-head-sha',
  'bad-pr',
  'bad-repo',
  'confirmation-expired',
  'different-target',
  'ledger-full',
  'no-pending-action',
  'not-a-confirmation',
  'replayed-confirmation',
  'replayed-utterance',
  'same-utterance',
  'unresolved-target',
  'verb-target-mismatch',
]);

/** The refusals a confirm can answer with when more than one read-back is outstanding. */
const CROWDED_CONFIRM_REFUSALS = Object.freeze([
  ...ID_TEXT_REFUSALS,
  'ambiguous-pending',
  'not-a-confirmation',
]);

/**
 * The refusal codes. Each names one family of refusal, so a caller can branch on
 * the reason rather than on the absence of a state. The field of the value
 * readers (`wrong-type`, `empty-string`, `too-long`, `bad-key`, `bad-character`,
 * `unknown-field`, `missing-field`) is spelled exactly as `shared/mailbox-schema`
 * spells it, so a reader of both does not have to learn two vocabularies.
 */
export const REFUSAL = Object.freeze({
  /** The value is not a plain object: null, an array, a primitive, an instance. */
  NOT_AN_OBJECT: 'not-an-object',
  /** A field this module does not name is present. */
  UNKNOWN_FIELD: 'unknown-field',
  /** A required field is absent or explicitly undefined. */
  MISSING_FIELD: 'missing-field',
  /** A field read through a getter -- refused without ever calling it. */
  ACCESSOR_PROPERTY: 'accessor-property',
  /** An input that threw while being read (a revoked or throwing Proxy). */
  INTERNAL_ERROR: 'internal-error',
  /** A field holds a value of the wrong type. */
  WRONG_TYPE: 'wrong-type',
  /** A required string is empty. */
  EMPTY_STRING: 'empty-string',
  /** A string is longer than its bound. */
  TOO_LONG: 'too-long',
  /** An id contains a character a database key may not contain. */
  BAD_KEY: 'bad-key',
  /** A string contains a control character or a lone surrogate. */
  BAD_CHARACTER: 'bad-character',
  /** A `now` or an `at` that is not an epoch-millisecond integer in 1 .. TIME_MAX. */
  INVALID_NOW: 'invalid-now',
  /** A `now` earlier than the state's own. */
  NOW_WENT_BACKWARDS: 'now-went-backwards',
  /** A `now` more than MAX_FORWARD_JUMP_MS ahead of the state's own. */
  NOW_JUMPED: 'now-jumped',
  /** A `phase` that is not one of the three. */
  UNKNOWN_STATE: 'unknown-state',
  /** A declared phase that disagrees with the pending read-backs. */
  INVALID_STATE: 'invalid-state',
  /** A `type` that is not one of the three events. */
  UNKNOWN_EVENT: 'unknown-event',
  /** A `timeoutMs` outside MIN_CONFIRM_TIMEOUT_MS .. MAX_CONFIRM_TIMEOUT_MS. */
  INVALID_TIMEOUT: 'invalid-timeout',
  /** A `maxPending` outside MIN_MAX_PENDING .. MAX_MAX_PENDING. */
  INVALID_MAX_PENDING: 'invalid-max-pending',
  /** A `maxLedger` outside MIN_MAX_LEDGER .. MAX_MAX_LEDGER. */
  INVALID_MAX_LEDGER: 'invalid-max-ledger',
  /** A `pending` that is not a plain array, or is longer than the state's cap. */
  INVALID_PENDING: 'invalid-pending',
  /** A pending record with a missing, unknown or malformed field. */
  INVALID_PENDING_ENTRY: 'invalid-pending-entry',
  /** Two pending records for one action id. */
  DUPLICATE_PENDING_ENTRY: 'duplicate-pending-entry',
  /** Pending records not in the canonical order. */
  UNSORTED_PENDING: 'unsorted-pending',
  /** A `ledger` that is not a plain array, or is longer than the state's cap. */
  INVALID_LEDGER: 'invalid-ledger',
  /** A ledger record with a missing, unknown or malformed field. */
  INVALID_LEDGER_ENTRY: 'invalid-ledger-entry',
  /** Two ledger records for one (kind, id). */
  DUPLICATE_LEDGER_ENTRY: 'duplicate-ledger-entry',
  /** Ledger records not in the canonical order. */
  UNSORTED_LEDGER: 'unsorted-ledger',
  /** A verb outside the closed set. */
  UNKNOWN_VERB: 'unknown-verb',
  /** A verb the ruling refuses outright: the kill switch, budget writes, deletes. */
  REFUSED_VERB: 'refused-verb',
  /** A destructive verb with no resolved target: nothing to read back. */
  UNRESOLVED_TARGET: 'unresolved-target',
  /** A verb handed an argument of a kind it does not take. */
  VERB_TARGET_MISMATCH: 'verb-target-mismatch',
  /** A repository that is not `owner/name`. */
  BAD_REPO: 'bad-repo',
  /** A pull-request number that is not a positive integer inside its bound. */
  BAD_PR: 'bad-pr',
  /** A head SHA that is not 40 or 64 lowercase hex characters. */
  BAD_HEAD_SHA: 'bad-head-sha',
  /** A confirming utterance that is not the fixed phrase. */
  NOT_A_CONFIRMATION: 'not-a-confirmation',
  /** One utterance carrying a command and a confirm token. */
  COMMAND_AND_CONFIRM: 'command-and-confirm',
  /** A confirm whose action id names no outstanding read-back. */
  NO_PENDING_ACTION: 'no-pending-action',
  /** A confirm with more than one read-back outstanding: it names no one of them. */
  AMBIGUOUS_PENDING: 'ambiguous-pending',
  /** A confirm asserting a different target than the one read back. */
  DIFFERENT_TARGET: 'different-target',
  /** A confirm past the read-back's timeout. */
  CONFIRMATION_EXPIRED: 'confirmation-expired',
  /** A confirming utterance that is the one that issued the command. */
  SAME_UTTERANCE: 'same-utterance',
  /** An utterance id already spent. */
  REPLAYED_UTTERANCE: 'replayed-utterance',
  /** An action id already spent. */
  REPLAYED_ACTION: 'replayed-action',
  /** A confirmation id already spent. */
  REPLAYED_CONFIRMATION: 'replayed-confirmation',
  /** A command with no room for another read-back. */
  PENDING_FULL: 'pending-full',
  /** No room in the id ledger, and nothing inside it is ever forgotten to make room. */
  LEDGER_FULL: 'ledger-full',
});

/**
 * The refusals that are about the *input* rather than about the answer: a state
 * that is not a state, an event that is not an event, a `now` outside its
 * window, a ledger that disagrees with itself. They belong to no cell, because
 * they are refused before the machine's phase can matter -- which is why the
 * test can require every other code to be declared in a cell and *reached* by a
 * probe, and still account for these.
 */
export const INPUT_REFUSALS = Object.freeze([
  REFUSAL.ACCESSOR_PROPERTY,
  REFUSAL.DUPLICATE_LEDGER_ENTRY,
  REFUSAL.DUPLICATE_PENDING_ENTRY,
  REFUSAL.INTERNAL_ERROR,
  REFUSAL.INVALID_LEDGER,
  REFUSAL.INVALID_LEDGER_ENTRY,
  REFUSAL.INVALID_MAX_LEDGER,
  REFUSAL.INVALID_MAX_PENDING,
  REFUSAL.INVALID_NOW,
  REFUSAL.INVALID_PENDING,
  REFUSAL.INVALID_PENDING_ENTRY,
  REFUSAL.INVALID_STATE,
  REFUSAL.INVALID_TIMEOUT,
  REFUSAL.MISSING_FIELD,
  REFUSAL.NOT_AN_OBJECT,
  REFUSAL.NOW_JUMPED,
  REFUSAL.NOW_WENT_BACKWARDS,
  REFUSAL.UNKNOWN_EVENT,
  REFUSAL.UNKNOWN_FIELD,
  REFUSAL.UNKNOWN_STATE,
  REFUSAL.UNSORTED_LEDGER,
  REFUSAL.UNSORTED_PENDING,
]);

/**
 * The transition table, as data: every phase x every event, with the outcomes it
 * may answer with, the refusals it may answer with, and the phases it may leave
 * the machine in. It is the machine's own declaration; the test writes the
 * expected table by hand -- never importing this one -- and asserts the two
 * agree, then drives literal states through every event to check both
 * directions: every declared cell is reached, and nothing else is.
 *
 * A `next` list is a *summary*: `idle` x `command` can stay `idle` (a launch) or
 * become `pending` (a read-back), and `abort` always leaves `idle` because it
 * only ever forgets.
 */
export const TRANSITIONS = Object.freeze({
  [PHASE.IDLE]: Object.freeze({
    [EVENT.COMMAND]: Object.freeze({
      ok: Object.freeze([OUTCOME.PASS_THROUGH, OUTCOME.READ_BACK]),
      no: COMMAND_REFUSALS,
      next: Object.freeze([PHASE.IDLE, PHASE.PENDING]),
    }),
    [EVENT.CONFIRM]: Object.freeze({
      ok: Object.freeze([]),
      no: IDLE_CONFIRM_REFUSALS,
      next: Object.freeze([PHASE.IDLE]),
    }),
    [EVENT.ABORT]: Object.freeze({
      ok: Object.freeze([OUTCOME.NOTHING_PENDING]),
      no: ABORT_REFUSALS,
      next: Object.freeze([PHASE.IDLE]),
    }),
  }),
  [PHASE.PENDING]: Object.freeze({
    [EVENT.COMMAND]: Object.freeze({
      ok: Object.freeze([OUTCOME.PASS_THROUGH, OUTCOME.READ_BACK]),
      no: COMMAND_FULL_REFUSALS,
      next: Object.freeze([PHASE.PENDING, PHASE.CROWDED]),
    }),
    [EVENT.CONFIRM]: Object.freeze({
      ok: Object.freeze([OUTCOME.CONFIRMED]),
      no: PENDING_CONFIRM_REFUSALS,
      next: Object.freeze([PHASE.IDLE, PHASE.PENDING]),
    }),
    [EVENT.ABORT]: Object.freeze({
      // "cancel that" can only ever *forget* read-backs, never authorise one, so
      // the ambiguous case resolves to "none of them" rather than a refusal --
      // which is also how a crowded machine is left again. A refusal leaves the
      // machine where it was, so the phase it was in is a next phase too.
      ok: Object.freeze([OUTCOME.DISCARDED, OUTCOME.NOTHING_PENDING]),
      no: ABORT_REFUSALS,
      next: Object.freeze([PHASE.IDLE, PHASE.PENDING]),
    }),
  }),
  [PHASE.CROWDED]: Object.freeze({
    [EVENT.COMMAND]: Object.freeze({
      ok: Object.freeze([OUTCOME.PASS_THROUGH, OUTCOME.READ_BACK]),
      no: COMMAND_FULL_REFUSALS,
      next: Object.freeze([PHASE.PENDING, PHASE.CROWDED]),
    }),
    [EVENT.CONFIRM]: Object.freeze({
      // A crowded machine refuses every confirm, whatever it carries: the only
      // refusals reachable here are the ones checked before the ambiguity.
      ok: Object.freeze([]),
      no: CROWDED_CONFIRM_REFUSALS,
      next: Object.freeze([PHASE.CROWDED]),
    }),
    [EVENT.ABORT]: Object.freeze({
      ok: Object.freeze([OUTCOME.DISCARDED, OUTCOME.NOTHING_PENDING]),
      no: ABORT_REFUSALS,
      next: Object.freeze([PHASE.CROWDED, PHASE.IDLE]),
    }),
  }),
});

/**
 * Which target kinds each destructive verb takes: P6.3's verb/target rule. A
 * cancel names a run, a merge a repository, PR and head, and a confirm either
 * a run or a repository and PR -- never a head, which P6.3 refuses on it.
 */
const VERB_TARGET_KINDS = Object.freeze({
  [VERB.CANCEL]: Object.freeze([TARGET_KIND.RUN]),
  [VERB.CONFIRM_ACTION]: Object.freeze([TARGET_KIND.RUN, TARGET_KIND.PR]),
  [VERB.MERGE]: Object.freeze([TARGET_KIND.MERGE]),
});

const VERB_VALUES = Object.freeze([...DESTRUCTIVE_VERBS, ...PASS_THROUGH_VERBS, ...REFUSED_VERBS]);

/**
 * initialState builds the empty machine.
 *
 * `now` is required and is the caller's (an injected clock): the module reads no
 * clock of its own. `timeoutMs`, `maxPending` and `maxLedger` are the caller's
 * knobs, each bounded, each refused rather than clamped.
 *
 * @param {{ now: number, timeoutMs?: number, maxPending?: number, maxLedger?: number }} options
 * @returns {{ ok: true, state: object } | { ok: false, code: string, field: string | null }}
 */
export function initialState(options) {
  try {
    return initialStateUnguarded(options);
  } catch {
    return refuse(REFUSAL.INTERNAL_ERROR, null);
  }
}

function initialStateUnguarded(options) {
  const read = readFields(options, ['now', 'timeoutMs', 'maxPending', 'maxLedger'], 'options');
  if (!read.ok) return read;
  const values = read.values;

  if (values.now === undefined) return refuse(REFUSAL.MISSING_FIELD, 'now');
  if (!isIntegerInRange(values.now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');

  const timeoutMs = values.timeoutMs === undefined ? DEFAULT_CONFIRM_TIMEOUT_MS : values.timeoutMs;
  if (!isIntegerInRange(timeoutMs, MIN_CONFIRM_TIMEOUT_MS, MAX_CONFIRM_TIMEOUT_MS)) {
    return refuse(REFUSAL.INVALID_TIMEOUT, 'timeoutMs');
  }
  const maxPending = values.maxPending === undefined ? DEFAULT_MAX_PENDING : values.maxPending;
  if (!isIntegerInRange(maxPending, MIN_MAX_PENDING, MAX_MAX_PENDING)) {
    return refuse(REFUSAL.INVALID_MAX_PENDING, 'maxPending');
  }
  const maxLedger = values.maxLedger === undefined ? DEFAULT_MAX_LEDGER : values.maxLedger;
  if (!isIntegerInRange(maxLedger, MIN_MAX_LEDGER, MAX_MAX_LEDGER)) {
    return refuse(REFUSAL.INVALID_MAX_LEDGER, 'maxLedger');
  }

  return {
    ok: true,
    state: buildState(PHASE.IDLE, values.now, timeoutMs, maxPending, maxLedger, [], []),
  };
}

/**
 * step applies one event to one state.
 *
 *     step(state, { type: 'command', now, utteranceId, text, verb, target, actionId })
 *     step(state, { type: 'confirm', now, utteranceId, text, actionId, confirmationId, target })
 *     step(state, { type: 'abort',   now, utteranceId, text })
 *
 * Every event carries the injected `now`. It returns `{ ok: true, outcome,
 * phase, ... , state }` or a refusal `{ ok: false, code, field }`. A refusal
 * carries **no state**: the caller keeps its own, and nothing the refused
 * utterance could have spent is spent. It never throws -- an exception from a
 * hostile input (a revoked or throwing Proxy) is answered as `internal-error` --
 * never mutates its inputs, and never reads a clock.
 *
 * The order of the checks is the rule, and it is what the README states:
 *
 *   1  the state                a state that disagrees with itself is refused
 *   2  the event's shape        its field set, `type`, and `now`, in 1..TIME_MAX
 *   3  the `now` window         no backwards, no leap past MAX_FORWARD_JUMP_MS
 *   4  the event's own fields   ids, text, verb, and the target's shape
 *   5  what it says             the fixed phrase, or the verb
 *   6  the machine's state      how many read-backs are outstanding, and whose
 *   7  the replay ledger        every id it spends, once
 *   8  target, then timeout     what would happen, and whether it still can
 *   9  the ledger's room        only a would-succeed event is refused for space
 *  10  accept
 *
 * The effect: everything that can be said about an utterance is said before
 * anything is spent or refused for lack of room, so a caller hears the real
 * reason. A verb the ruling refuses is refused before its target is read, so a
 * kill switch with a plausible target is still refused and its target is never
 * echoed. A launch is answered before any of the machine's state is consulted,
 * because D162's answer does not depend on it.
 *
 * @param {object} state the machine, as returned by this function or initialState
 * @param {object} event the event, with its own injected `now`
 * @returns {{ ok: true, outcome: string, phase: string, state: object } | { ok: false, code: string, field: string | null }}
 */
export function step(state, event) {
  try {
    return stepUnguarded(state, event);
  } catch {
    // A Proxy can throw from any trap, and a revoked one throws from all of
    // them. That is a refusal like any other, and what was thrown is never
    // echoed: it may carry whatever the input put in it.
    return refuse(REFUSAL.INTERNAL_ERROR, null);
  }
}

function stepUnguarded(state, event) {
  const current = readState(state);
  if (!current.ok) return current;

  const read = readEvent(event);
  if (!read.ok) return read;

  const values = read.values;
  if (values.now < current.state.now) {
    // The timeout is a function of the pending time and this `now`, and the
    // ledger is what makes a replay permanent. A `now` that goes backwards
    // would make an expired read-back live again, so it is refused rather than
    // absorbed.
    return refuse(REFUSAL.NOW_WENT_BACKWARDS, 'now');
  }
  if (values.now - current.state.now > MAX_FORWARD_JUMP_MS) {
    // `now` only moves forward, so one far-future `now` would otherwise refuse
    // every later honest one as `now-went-backwards` for ever. A leap past the
    // bound is refused instead, and the state keeps its own `now`.
    return refuse(REFUSAL.NOW_JUMPED, 'now');
  }
  if (!Object.hasOwn(HANDLERS, values.type)) return refuse(REFUSAL.UNKNOWN_EVENT, 'type');
  return HANDLERS[values.type](current.state, values);
}

// ---------------------------------------------------------------------------
// The handlers
// ---------------------------------------------------------------------------

/**
 * command: an utterance the router read as one of the app's commands.
 *
 * A refusal here spends nothing: the utterance id is recorded only once the
 * machine has decided to answer it.
 */
function command(state, values) {
  const fields = readCommandFields(values);
  if (!fields.ok) return fields;

  const { utteranceId, text, verb, actionId } = fields.values;
  const target = values.target;

  if (carriesConfirmToken(text)) {
    // D47's Rule: the same misrecognition must not satisfy both halves. An
    // utterance that carries the confirming phrase beside a command is refused
    // rather than read as either.
    return refuse(REFUSAL.COMMAND_AND_CONFIRM, 'text');
  }
  if (!VERB_VALUES.includes(verb)) return refuse(REFUSAL.UNKNOWN_VERB, 'verb');
  if (REFUSED_VERBS.includes(verb)) {
    // The kill switch, the budget writes, the deletes: refused outright by the
    // 2026-10-17 ruling, whatever the target says. Refused before the target is
    // read, so nothing about it is echoed and nothing about it is trusted.
    return refuse(REFUSAL.REFUSED_VERB, 'verb');
  }

  const kinds = VERB_TARGET_KINDS[verb];
  if (kinds === undefined) {
    // A pass-through verb takes no target and no action id: a launch is not read
    // back, so carrying either is a caller that thinks it is.
    if (target !== undefined) return refuse(REFUSAL.VERB_TARGET_MISMATCH, 'target');
    if (actionId !== undefined) return refuse(REFUSAL.VERB_TARGET_MISMATCH, 'actionId');
  } else if (actionId === undefined) {
    // A destructive verb must carry the action id the confirmation will name:
    // without it there is no id for "one confirmation authorises exactly one
    // action" to be about, so the command is malformed rather than read back
    // with an action nobody can confirm.
    return refuse(REFUSAL.MISSING_FIELD, 'actionId');
  }

  // P6.2: an unresolved target is refused and cannot be read back, so it can
  // never be confirmed. Ambiguity is the same refusal: the router resolves
  // against the configured lists and hands in nothing when it cannot.
  // readResolvedTarget is the one place that says so, so a command and a
  // confirmation cannot disagree about what "no target" means.

  let resolved = null;
  if (kinds !== undefined) {
    const readTarget = readResolvedTarget(target, kinds);
    if (!readTarget.ok) return readTarget;
    resolved = readTarget.target;
  }

  if (hasLedgerId(state, LEDGER_KIND.UTTERANCE, utteranceId)) {
    return refuse(REFUSAL.REPLAYED_UTTERANCE, 'utteranceId');
  }
  if (kinds !== undefined && hasLedgerId(state, LEDGER_KIND.ACTION, actionId)) {
    // Two commands under one action id are an ambiguity about which action a
    // confirmation authorised, so the second is refused rather than allowed to
    // overwrite the first.
    return refuse(REFUSAL.REPLAYED_ACTION, 'actionId');
  }

  if (kinds === undefined) {
    // D162: a launch goes straight through. Its utterance is still spent, so
    // "one utterance, one event" holds for it too -- a pass-through authorises
    // nothing, but its id is not a second utterance later.
    if (state.ledger.length + 1 > state.maxLedger) return refuse(REFUSAL.LEDGER_FULL, 'ledger');
    return Object.freeze({
      ok: true,
      outcome: OUTCOME.PASS_THROUGH,
      phase: state.phase,
      verb,
      actionId: null,
      readBack: null,
      state: buildState(
        state.phase,
        values.now,
        state.timeoutMs,
        state.maxPending,
        state.maxLedger,
        state.pending,
        spend(state.ledger, [{ kind: LEDGER_KIND.UTTERANCE, id: utteranceId, at: values.now }]),
      ),
    });
  }

  if (state.ledger.length + 2 > state.maxLedger) return refuse(REFUSAL.LEDGER_FULL, 'ledger');

  // A read-back that timed out is dropped here rather than kept: the pending set
  // must stay bounded, and a command is the one event that may always add one.
  // A *confirm* never sweeps, because a sweep there would answer
  // `no-pending-action` where the plan requires the timeout to be named.
  const live = sweep(state.pending, values.now);
  if (live.length + 1 > state.maxPending) return refuse(REFUSAL.PENDING_FULL, 'pending');

  const at = values.now;
  const deadline = at + state.timeoutMs;
  const entry = Object.freeze({ actionId, verb, target: resolved, at, deadline, by: utteranceId });
  const pendings = ordered([...live, entry], comparePending);

  return Object.freeze({
    ok: true,
    outcome: OUTCOME.READ_BACK,
    phase: phaseOf(pendings),
    verb,
    actionId,
    target: resolved,
    at,
    deadline,
    readBack: readBack(verb, resolved),
    state: buildState(
      phaseOf(pendings),
      at,
      state.timeoutMs,
      state.maxPending,
      state.maxLedger,
      pendings,
      spend(state.ledger, [
        { kind: LEDGER_KIND.UTTERANCE, id: utteranceId, at },
        { kind: LEDGER_KIND.ACTION, id: actionId, at },
      ]),
    ),
  });
}

/**
 * confirm: the fixed phrase, as its own utterance.
 *
 * Everything that can be said about *this* confirmation is said before the
 * timeout and before the ledger's room, so a caller hears the real reason rather
 * than "no space".
 */
function confirm(state, values) {
  const fields = readConfirmFields(values);
  if (!fields.ok) return fields;

  const { utteranceId, text, actionId, confirmationId } = fields.values;
  const target = values.target;

  if (!isConfirmPhrase(text)) {
    // "conform", "confirmed it", "yes", "sure", "yes, merge it" (G10): each is
    // refused -- the phrase is fixed, short and low-confusability, and nothing
    // this module does repairs a misrecognition into a confirmation.
    return refuse(REFUSAL.NOT_A_CONFIRMATION, 'text');
  }
  if (state.pending.length > 1) {
    // P6.2: "a confirm for a different target (the pending action changed, or a
    // second pending action) is refused". A speaker with two read-backs
    // outstanding has not said which one, so nothing is authorised.
    return refuse(REFUSAL.AMBIGUOUS_PENDING, 'pending');
  }

  const entry = state.pending[0];
  if (entry === undefined || entry.actionId !== actionId) {
    // "one confirmation authorises exactly one action (it carries the action
    // id)": an id naming no outstanding read-back confirms nothing, and an
    // identical confirmation replayed after its action was consumed arrives
    // here, because that read-back is gone.
    return refuse(REFUSAL.NO_PENDING_ACTION, 'actionId');
  }
  if (entry.by === utteranceId) {
    // "the utterance that issued a command can never confirm it".
    return refuse(REFUSAL.SAME_UTTERANCE, 'utteranceId');
  }
  if (hasLedgerId(state, LEDGER_KIND.UTTERANCE, utteranceId)) {
    // A spent utterance is spent: it authorises nothing, whatever it repeats.
    return refuse(REFUSAL.REPLAYED_UTTERANCE, 'utteranceId');
  }
  if (hasLedgerId(state, LEDGER_KIND.CONFIRMATION, confirmationId)) {
    // A spent confirmation is spent against every later action, not only the one
    // it confirmed: that is what "one confirmation authorises exactly one
    // action" means once the ledger remembers it.
    return refuse(REFUSAL.REPLAYED_CONFIRMATION, 'confirmationId');
  }

  const readTarget = readResolvedTarget(target, VERB_TARGET_KINDS[entry.verb]);
  if (!readTarget.ok) return readTarget;
  if (!sameTarget(entry.target, readTarget.target)) {
    // The pending action changed, or -- for a merge -- the PR's head moved since
    // the read-back. Either way the read-back the speaker heard is not what
    // would happen now, so the confirmation is refused (D169).
    return refuse(REFUSAL.DIFFERENT_TARGET, 'target');
  }
  if (values.now > entry.deadline) {
    return refuse(REFUSAL.CONFIRMATION_EXPIRED, 'now');
  }
  if (state.ledger.length + 2 > state.maxLedger) return refuse(REFUSAL.LEDGER_FULL, 'ledger');

  const pendings = state.pending.filter((one) => one.actionId !== entry.actionId);

  // Everything authorised is the target that was READ BACK -- the pending
  // record's, never the one the confirmation asserted. The assertion (for a
  // merge, the head re-read at confirmation time) is only ever compared with
  // it above, so a confirmation cannot substitute a target the speaker did not
  // hear, and the record binds the head the read-back named.
  return Object.freeze({
    ok: true,
    outcome: OUTCOME.CONFIRMED,
    phase: phaseOf(pendings),
    verb: entry.verb,
    actionId: entry.actionId,
    confirmationId,
    target: entry.target,
    confirmedAt: values.now,
    readBack: readBack(entry.verb, entry.target),
    record: actionRecord(entry, confirmationId, values.now),
    state: buildState(
      phaseOf(pendings),
      values.now,
      state.timeoutMs,
      state.maxPending,
      state.maxLedger,
      pendings,
      spend(state.ledger, [
        { kind: LEDGER_KIND.UTTERANCE, id: utteranceId, at: values.now },
        { kind: LEDGER_KIND.CONFIRMATION, id: confirmationId, at: values.now },
      ]),
    ),
  });
}

/**
 * abort: "cancel that".
 *
 * It can only ever forget read-backs, never authorise one, so it is the way out
 * of a crowded machine: every outstanding read-back is dropped, and "nothing was
 * outstanding" is an outcome rather than a refusal. Its utterance is spent like
 * any other.
 */
function abort(state, values) {
  const fields = readAbortFields(values);
  if (!fields.ok) return fields;

  const { utteranceId, text } = fields.values;

  if (carriesConfirmToken(text)) return refuse(REFUSAL.COMMAND_AND_CONFIRM, 'text');
  if (hasLedgerId(state, LEDGER_KIND.UTTERANCE, utteranceId)) {
    return refuse(REFUSAL.REPLAYED_UTTERANCE, 'utteranceId');
  }
  if (state.ledger.length + 1 > state.maxLedger) return refuse(REFUSAL.LEDGER_FULL, 'ledger');

  const live = sweep(state.pending, values.now);

  return Object.freeze({
    ok: true,
    outcome: live.length === 0 ? OUTCOME.NOTHING_PENDING : OUTCOME.DISCARDED,
    phase: PHASE.IDLE,
    actionIds: Object.freeze(live.map((one) => one.actionId)),
    state: buildState(
      PHASE.IDLE,
      values.now,
      state.timeoutMs,
      state.maxPending,
      state.maxLedger,
      [],
      spend(state.ledger, [{ kind: LEDGER_KIND.UTTERANCE, id: utteranceId, at: values.now }]),
    ),
  });
}

/**
 * actionRecord builds P6.3's action record (`message.action`) for a confirmed
 * read-back, field for field: `{ id, verb, target, head_sha?, confirmation }`.
 * The target is the one read back; a merge carries the head the read-back
 * named as `head_sha`, which the consumer compares with the PR's head before it
 * merges (P6.7). Frozen all the way down.
 */
function actionRecord(entry, confirmationId, confirmedAt) {
  const t = entry.target;
  const target =
    t.kind === TARGET_KIND.RUN ? Object.freeze({ run: t.runId }) : Object.freeze({ repo: t.repo, pr: t.pr });
  return Object.freeze({
    id: entry.actionId,
    verb: entry.verb,
    target,
    ...(t.kind === TARGET_KIND.MERGE ? { head_sha: t.headSha } : {}),
    confirmation: Object.freeze({ id: confirmationId, at: confirmedAt }),
  });
}

/** The one dispatcher, keyed by the closed event set. */
const HANDLERS = Object.freeze({ [EVENT.COMMAND]: command, [EVENT.CONFIRM]: confirm, [EVENT.ABORT]: abort });

// ---------------------------------------------------------------------------
// The read-back, and what the screen reads
// ---------------------------------------------------------------------------

/**
 * readBack builds the spoken read-back for a resolved target.
 *
 * It takes the verb and the target and **nothing else** -- no transcript, no
 * name the speaker used -- because D47's Rule requires the read-back to name the
 * exact run id (or the repository and PR) rather than a paraphrase: a run the
 * speaker *called* "the docs refactor" must not make the read-back say "the docs
 * refactor". It returns null when the verb and the target do not agree, so a
 * caller cannot compose a read-back for a pair this machine would refuse.
 *
 * @param {string} verb one of the destructive verbs
 * @param {object} target a resolved target of the verb's own kind
 * @returns {string | null} the read-back to speak, or null
 */
export function readBack(verb, target) {
  try {
    const kinds = VERB_TARGET_KINDS[verb];
    if (kinds === undefined) return null;
    const read = readResolvedTarget(target, kinds);
    if (!read.ok) return null;
    const resolved = read.target;
    let what;
    if (resolved.kind === TARGET_KIND.MERGE) {
      what = `Merge ${resolved.repo}#${resolved.pr} at head ${resolved.headSha}.`;
    } else if (verb === VERB.CANCEL) {
      what = `Cancel run ${resolved.runId}.`;
    } else if (resolved.kind === TARGET_KIND.RUN) {
      what = `Confirm run ${resolved.runId}.`;
    } else {
      what = `Confirm ${resolved.repo}#${resolved.pr}.`;
    }
    return `${what} ${CONFIRM_CUE}`;
  } catch {
    return null;
  }
}

/**
 * isConfirmPhrase decides whether an utterance is the fixed phrase.
 *
 * It is **P6.1's answer**, not a second grammar: the utterance is routed by
 * `shared/command`'s router (with its default wake-word aliases) and is the
 * phrase only when the route is the `confirm` app command. So "Fugaro,
 * confirm" and "Figaro, confirm." are the phrase, and a bare "confirm" is not
 * -- P6.1 forwards that to the agent as text (D164), and a machine that also
 * read it as a confirmation would act on words the agent received too. Every
 * near miss ("conform", "confirmed it", "yes", "Fugaro, confirm it") and any
 * utterance carrying an invisible format character is refused by the router.
 *
 * @param {unknown} text a candidate utterance
 * @returns {boolean} true when it is the fixed phrase, and nothing else
 */
export function isConfirmPhrase(text) {
  if (typeof text !== 'string') return false;
  return isConfirmation(routeUtterance({ utterance: text }));
}

/**
 * pending lists the read-backs still inside their timeout, for the screen
 * (P6.12) and the text fallback (P6.9).
 *
 * Each entry's `readBack` is **recomputed** from its verb and target rather than
 * read from the state, so a state handed back with an edited read-back cannot be
 * spoken -- the one string a human is asked to trust is never trusted from the
 * value it is stored in.
 *
 * @param {object} state the machine
 * @param {number} now the caller's clock
 * @returns {{ ok: true, actions: ReadonlyArray<object> } | { ok: false, code: string, field: string | null }}
 */
export function pending(state, now) {
  try {
    const current = readState(state);
    if (!current.ok) return current;
    if (!isIntegerInRange(now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');
    return {
      ok: true,
      actions: Object.freeze(
        sweep(current.state.pending, now).map((one) =>
          Object.freeze({
            actionId: one.actionId,
            verb: one.verb,
            target: one.target,
            at: one.at,
            deadline: one.deadline,
            readBack: readBack(one.verb, one.target),
          }),
        ),
      ),
    };
  } catch {
    return refuse(REFUSAL.INTERNAL_ERROR, null);
  }
}

// ---------------------------------------------------------------------------
// The event's field readers
// ---------------------------------------------------------------------------

// readEvent reads the event as a whole: its field set, `type` and `now`.
function readEvent(event) {
  const type = isPlainObject(event) ? ownValue(event, 'type') : undefined;
  const names =
    type === EVENT.COMMAND
      ? COMMAND_FIELDS
      : type === EVENT.CONFIRM
        ? CONFIRM_FIELDS
        : type === EVENT.ABORT
          ? ABORT_FIELDS
          : // An unknown `type` is refused below by `unknown-event`; until then
            // the union is the polite set to read, so that the refusal is about
            // the type rather than about a field such an event happens to carry.
            UNION_FIELDS;

  const read = readFields(event, names, 'event');
  if (!read.ok) return read;
  const values = read.values;

  if (values.type === undefined) return refuse(REFUSAL.MISSING_FIELD, 'type');
  if (typeof values.type !== 'string') return refuse(REFUSAL.WRONG_TYPE, 'type');
  if (values.now === undefined) return refuse(REFUSAL.MISSING_FIELD, 'now');
  if (!isIntegerInRange(values.now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');
  return { ok: true, values };
}

const UNION_FIELDS = Object.freeze([...new Set([...COMMAND_FIELDS, ...CONFIRM_FIELDS, ...ABORT_FIELDS])]);

// readCommandFields validates a command's own fields once each, in a fixed
// order, so a getter cannot answer differently between a check and its use.
function readCommandFields(values) {
  const utteranceId = keyIdField(values.utteranceId, 'utteranceId');
  if (utteranceId.bad) return utteranceId.bad;
  const text = stringField(values.text, 'text', TEXT_MAX);
  if (text.bad) return text.bad;
  const verb = stringField(values.verb, 'verb', ID_MAX);
  if (verb.bad) return verb.bad;
  if (values.actionId === undefined) {
    return {
      ok: true,
      values: { utteranceId: utteranceId.value, text: text.value, verb: verb.value, actionId: undefined },
    };
  }
  const actionId = keyIdField(values.actionId, 'actionId');
  if (actionId.bad) return actionId.bad;
  return {
    ok: true,
    values: { utteranceId: utteranceId.value, text: text.value, verb: verb.value, actionId: actionId.value },
  };
}

// readConfirmFields validates a confirm's own fields once each.
function readConfirmFields(values) {
  const utteranceId = keyIdField(values.utteranceId, 'utteranceId');
  if (utteranceId.bad) return utteranceId.bad;
  const text = stringField(values.text, 'text', TEXT_MAX);
  if (text.bad) return text.bad;
  const actionId = keyIdField(values.actionId, 'actionId');
  if (actionId.bad) return actionId.bad;
  const confirmationId = keyIdField(values.confirmationId, 'confirmationId');
  if (confirmationId.bad) return confirmationId.bad;
  return {
    ok: true,
    values: {
      utteranceId: utteranceId.value,
      text: text.value,
      actionId: actionId.value,
      confirmationId: confirmationId.value,
    },
  };
}

// readAbortFields validates an abort's own fields once each.
function readAbortFields(values) {
  const utteranceId = keyIdField(values.utteranceId, 'utteranceId');
  if (utteranceId.bad) return utteranceId.bad;
  const text = stringField(values.text, 'text', TEXT_MAX);
  if (text.bad) return text.bad;
  return { ok: true, values: { utteranceId: utteranceId.value, text: text.value } };
}

/**
 * readResolvedTarget reads a resolved target of the kind the verb requires.
 *
 * The target is the one value a confirmation is about, so it is read strictly:
 * the kind must be the verb's kind, the field set is the version (an unknown key
 * is refused), every value is bounded, and a repository must be `owner/name`
 * with both segments present. Nothing is normalised -- a repository is not
 * lower-cased and a SHA is not shortened -- because a target this module
 * repaired would be a target the consumer never saw.
 */
function readResolvedTarget(value, kinds) {
  if (value === undefined || value === null) return refuse(REFUSAL.UNRESOLVED_TARGET, 'target');
  if (!isPlainObject(value)) return refuse(REFUSAL.WRONG_TYPE, 'target');

  // `kind` is read on its own, before the field set: a target of another kind is
  // a mismatch, not an unknown field, and the refusal must say which of the two
  // it is.
  const kindDescriptor = Object.getOwnPropertyDescriptor(value, 'kind');
  if (kindDescriptor === undefined) return refuse(REFUSAL.MISSING_FIELD, 'target.kind');
  if (!Object.hasOwn(kindDescriptor, 'value')) return refuse(REFUSAL.ACCESSOR_PROPERTY, 'target.kind');
  if (!kinds.includes(kindDescriptor.value)) return refuse(REFUSAL.VERB_TARGET_MISMATCH, 'target.kind');
  const kind = kindDescriptor.value;

  const names =
    kind === TARGET_KIND.RUN
      ? RUN_TARGET_FIELDS
      : kind === TARGET_KIND.MERGE
        ? MERGE_TARGET_FIELDS
        : PR_TARGET_FIELDS;
  const read = readFields(value, names, 'target');
  if (!read.ok) return read;
  const target = read.values;

  if (kind === TARGET_KIND.RUN) {
    const runId = keyIdField(target.runId, 'target.runId');
    if (runId.bad) return runId.bad;
    // P6.3's run alphabet and shape: nothing the consumer's command line could
    // read as a flag or a path.
    if (!RUN_CHARS.test(runId.value) || !RUN_PATTERN.test(runId.value)) {
      return refuse(REFUSAL.BAD_KEY, 'target.runId');
    }
    return { ok: true, target: Object.freeze({ kind, runId: runId.value }) };
  }

  const repo = repoField(target.repo);
  if (repo.bad) return repo.bad;
  if (!isIntegerInRange(target.pr, 1, PR_MAX)) return refuse(REFUSAL.BAD_PR, 'target.pr');
  if (kind === TARGET_KIND.PR) {
    return { ok: true, target: Object.freeze({ kind, repo: repo.value, pr: target.pr }) };
  }
  const headSha = headShaField(target.headSha);
  if (headSha.bad) return headSha.bad;
  return {
    ok: true,
    target: Object.freeze({ kind, repo: repo.value, pr: target.pr, headSha: headSha.value }),
  };
}

const RUN_TARGET_FIELDS = Object.freeze(['kind', 'runId']);
const MERGE_TARGET_FIELDS = Object.freeze(['kind', 'repo', 'pr', 'headSha']);
const PR_TARGET_FIELDS = Object.freeze(['kind', 'repo', 'pr']);

// repoField validates `owner/name`.
function repoField(value) {
  if (typeof value !== 'string') return { bad: refuse(REFUSAL.WRONG_TYPE, 'target.repo') };
  if (value === '') return { bad: refuse(REFUSAL.EMPTY_STRING, 'target.repo') };
  if (value.length > REPO_MAX) return { bad: refuse(REFUSAL.TOO_LONG, 'target.repo') };
  const parts = value.split('/');
  if (parts.length !== 2 || parts.some((part) => !isRepoSegment(part))) {
    return { bad: refuse(REFUSAL.BAD_REPO, 'target.repo') };
  }
  return { value };
}

// isRepoSegment: a name GitHub would have, in P6.3's shape. Bounded, no `-`
// or `.` first, and no character that could be read as a path, a flag or a
// second repository.
function isRepoSegment(part) {
  return part.length > 0 && part.length <= REPO_SEGMENT_MAX && REPO_SEGMENT_PATTERN.test(part);
}

// headShaField validates the PR head read back at confirmation: 40 or 64
// lowercase hex characters, the two shapes git produces and GitHub returns.
function headShaField(value) {
  if (typeof value !== 'string') return { bad: refuse(REFUSAL.WRONG_TYPE, 'target.headSha') };
  if (value === '') return { bad: refuse(REFUSAL.EMPTY_STRING, 'target.headSha') };
  if (!/^(?:[0-9a-f]{40}|[0-9a-f]{64})$/.test(value)) {
    return { bad: refuse(REFUSAL.BAD_HEAD_SHA, 'target.headSha') };
  }
  return { value };
}

// ---------------------------------------------------------------------------
// The state reader
// ---------------------------------------------------------------------------

/**
 * readState reads a whole machine: the phase and the four knobs, the pending
 * read-backs and the id ledger.
 *
 * A state is refused rather than repaired whenever it disagrees with itself -- a
 * phase that is not the summary of its pendings, a deadline outside the timeout
 * bounds (a pending time the machine never mints), a ledger out of canonical
 * order -- because the answers this module gives are a function of the state it
 * is handed, and a state that can be read two ways would have two.
 */
function readState(value) {
  const read = readFields(value, STATE_FIELDS, 'state');
  if (!read.ok) return read;
  const values = read.values;

  for (const field of STATE_FIELDS) {
    if (values[field] === undefined) return refuse(REFUSAL.MISSING_FIELD, field);
  }
  if (!PHASE_VALUES.includes(values.phase)) return refuse(REFUSAL.UNKNOWN_STATE, 'phase');
  if (!isIntegerInRange(values.now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');
  if (!isIntegerInRange(values.timeoutMs, MIN_CONFIRM_TIMEOUT_MS, MAX_CONFIRM_TIMEOUT_MS)) {
    return refuse(REFUSAL.INVALID_TIMEOUT, 'timeoutMs');
  }
  if (!isIntegerInRange(values.maxPending, MIN_MAX_PENDING, MAX_MAX_PENDING)) {
    return refuse(REFUSAL.INVALID_MAX_PENDING, 'maxPending');
  }
  if (!isIntegerInRange(values.maxLedger, MIN_MAX_LEDGER, MAX_MAX_LEDGER)) {
    return refuse(REFUSAL.INVALID_MAX_LEDGER, 'maxLedger');
  }

  const pendings = readPending(values.pending, values.maxPending);
  if (!pendings.ok) return pendings;
  const ledger = readLedger(values.ledger, values.maxLedger);
  if (!ledger.ok) return ledger;

  if (values.phase !== phaseOf(pendings.entries)) return refuse(REFUSAL.INVALID_STATE, 'phase');

  return {
    ok: true,
    state: buildState(
      values.phase,
      values.now,
      values.timeoutMs,
      values.maxPending,
      values.maxLedger,
      pendings.entries,
      ledger.entries,
    ),
  };
}

/** readPending reads the pending read-backs: plain, bounded, unique, canonical. */
function readPending(value, maxPending) {
  const items = readArray(value, maxPending);
  if (items === null) return refuse(REFUSAL.INVALID_PENDING, 'pending');

  const entries = [];
  for (const raw of items) {
    const read = readFields(raw, PENDING_FIELDS, 'pending');
    if (!read.ok) return refuse(REFUSAL.INVALID_PENDING_ENTRY, 'pending');
    const entry = read.values;
    if (
      !isKeyId(entry.actionId) ||
      !DESTRUCTIVE_VERBS.includes(entry.verb) ||
      !isKeyId(entry.by) ||
      !isIntegerInRange(entry.at, 1, TIME_MAX) ||
      !isIntegerInRange(entry.deadline - entry.at, MIN_CONFIRM_TIMEOUT_MS, MAX_CONFIRM_TIMEOUT_MS) ||
      !isPlainObject(entry.target)
    ) {
      return refuse(REFUSAL.INVALID_PENDING_ENTRY, 'pending');
    }
    const target = readResolvedTarget(entry.target, VERB_TARGET_KINDS[entry.verb]);
    if (!target.ok) return refuse(REFUSAL.INVALID_PENDING_ENTRY, 'pending');
    entries.push(
      Object.freeze({
        actionId: entry.actionId,
        verb: entry.verb,
        target: target.target,
        at: entry.at,
        deadline: entry.deadline,
        by: entry.by,
      }),
    );
  }

  const seen = new Set();
  for (const entry of entries) {
    if (seen.has(entry.actionId)) return refuse(REFUSAL.DUPLICATE_PENDING_ENTRY, 'pending');
    seen.add(entry.actionId);
  }
  for (let index = 1; index < entries.length; index += 1) {
    if (comparePending(entries[index - 1], entries[index]) >= 0) {
      return refuse(REFUSAL.UNSORTED_PENDING, 'pending');
    }
  }
  return { ok: true, entries: Object.freeze(entries) };
}

/** readLedger reads the spent ids: plain, bounded, unique, canonical. */
function readLedger(value, maxLedger) {
  const items = readArray(value, maxLedger);
  if (items === null) return refuse(REFUSAL.INVALID_LEDGER, 'ledger');

  const entries = [];
  for (const raw of items) {
    const read = readFields(raw, LEDGER_FIELDS, 'ledger');
    if (!read.ok) return refuse(REFUSAL.INVALID_LEDGER_ENTRY, 'ledger');
    const entry = read.values;
    if (!LEDGER_KINDS.includes(entry.kind) || !isKeyId(entry.id) || !isIntegerInRange(entry.at, 1, TIME_MAX)) {
      return refuse(REFUSAL.INVALID_LEDGER_ENTRY, 'ledger');
    }
    entries.push(Object.freeze({ kind: entry.kind, id: entry.id, at: entry.at }));
  }

  const seen = new Set();
  for (const entry of entries) {
    const key = `${entry.kind}\u0000${entry.id}`;
    if (seen.has(key)) return refuse(REFUSAL.DUPLICATE_LEDGER_ENTRY, 'ledger');
    seen.add(key);
  }
  for (let index = 1; index < entries.length; index += 1) {
    if (compareLedger(entries[index - 1], entries[index]) >= 0) {
      return refuse(REFUSAL.UNSORTED_LEDGER, 'ledger');
    }
  }
  return { ok: true, entries: Object.freeze(entries) };
}

// ---------------------------------------------------------------------------
// The small predicates
// ---------------------------------------------------------------------------

/**
 * Whether a transcript carries the confirming phrase as one of its words.
 *
 * Words are split on whitespace **and punctuation**, because P6.1 strips
 * punctuation from a word before matching it: a command ending "confirm." or
 * "x,confirm" carries the phrase exactly as much as one ending "confirm".
 * Splitting wider than the router is the safe direction here -- this check
 * only ever refuses.
 */
function carriesConfirmToken(text) {
  return text
    .toLowerCase()
    .split(/[\s\p{P}\p{S}]+/u)
    .some((word) => CONFIRM_PHRASES.includes(word));
}

/** Whether the two resolved targets are the same target, field by field. */
function sameTarget(left, right) {
  if (left.kind !== right.kind) return false;
  if (left.kind === TARGET_KIND.RUN) return left.runId === right.runId;
  if (left.kind === TARGET_KIND.PR) return left.repo === right.repo && left.pr === right.pr;
  return left.repo === right.repo && left.pr === right.pr && left.headSha === right.headSha;
}

/** Whether the ledger already holds an id of this kind. */
function hasLedgerId(state, kind, id) {
  return state.ledger.some((entry) => entry.kind === kind && entry.id === id);
}

/** The read-backs still inside their timeout. At the deadline exactly, one is still inside. */
function sweep(pendings, now) {
  return pendings.filter((one) => now <= one.deadline);
}

/** The phase a set of read-backs implies, restated once here and once in the tests. */
function phaseOf(pendings) {
  if (pendings.length === 0) return PHASE.IDLE;
  if (pendings.length === 1) return PHASE.PENDING;
  return PHASE.CROWDED;
}

/** The canonical order of the pending read-backs: `at`, then `actionId`. */
function comparePending(left, right) {
  if (left.at !== right.at) return left.at < right.at ? -1 : 1;
  if (left.actionId === right.actionId) return 0;
  return left.actionId < right.actionId ? -1 : 1;
}

/** The canonical order of the ledger: `at`, then `kind`, then `id`. */
function compareLedger(left, right) {
  if (left.at !== right.at) return left.at < right.at ? -1 : 1;
  if (left.kind !== right.kind) return left.kind < right.kind ? -1 : 1;
  if (left.id === right.id) return 0;
  return left.id < right.id ? -1 : 1;
}

/** ordered returns a frozen copy in the canonical order. */
function ordered(entries, compare) {
  return Object.freeze([...entries].sort(compare));
}

/**
 * spend adds ledger records and returns the ledger in canonical order.
 *
 * The ledger is never truncated and never deduplicated: an id is in it or it is
 * not, and making room by forgetting is exactly the replay this module refuses.
 */
function spend(ledger, entries) {
  return ordered([...ledger, ...entries.map((entry) => Object.freeze({ ...entry }))], compareLedger);
}

/** buildState freezes the seven fields, in the one order they are read in. */
function buildState(phase, now, timeoutMs, maxPending, maxLedger, pendings, ledger) {
  return Object.freeze({
    phase,
    now,
    timeoutMs,
    maxPending,
    maxLedger,
    pending: Object.freeze([...pendings]),
    ledger: Object.freeze([...ledger]),
  });
}

// ---------------------------------------------------------------------------
// The value readers
// ---------------------------------------------------------------------------

/**
 * readFields reads exactly the named fields into a null-prototype snapshot,
 * each field once, through its own descriptor.
 *
 * A value that is not a plain data object is refused (`not-an-object`), a key
 * the caller does not name -- a symbol included -- is refused (`unknown-field`):
 * the field set is the version, and a state or an event from a later protocol
 * must be refused rather than read with the new field dropped. An accessor
 * property is refused (`accessor-property`) without ever being called, because a
 * getter can answer differently each time it is read, and this module's whole
 * claim is that the answer is a function of the value it was handed.
 *
 * An unknown key is reported by `where` -- the container this module named --
 * and never by the key itself: the key is the input's choice, and a refusal that
 * echoed it would log whatever a caller chose to put there.
 */
function readFields(value, names, where) {
  if (!isPlainObject(value)) return refuse(REFUSAL.NOT_AN_OBJECT, where);

  const values = Object.create(null);
  for (const key of Reflect.ownKeys(value)) {
    if (!names.includes(key)) return refuse(REFUSAL.UNKNOWN_FIELD, where);
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (descriptor === undefined || !Object.hasOwn(descriptor, 'value')) {
      return refuse(REFUSAL.ACCESSOR_PROPERTY, where);
    }
    values[key] = descriptor.value;
  }
  return { ok: true, values };
}

/**
 * readArray reads a plain array into a fresh one, or answers null.
 *
 * It is read by index, never iterated: an own `Symbol.iterator` would otherwise
 * decide which records this module sees -- a spent id could be skipped and a
 * replay admitted. So the array must be a plain one, its length is checked
 * against the cap before anything else, its own keys must be exactly the indices
 * and `length`, and every index must be a data property (a getter is never
 * called).
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

/** A plain data object, and not an instance of anything. */
function isPlainObject(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

/** One own property's value, or undefined -- used only to pick an event's field set. */
function ownValue(value, key) {
  const descriptor = Object.getOwnPropertyDescriptor(value, key);
  if (descriptor === undefined || !Object.hasOwn(descriptor, 'value')) return undefined;
  return descriptor.value;
}

/**
 * An integer inside a closed range, and never a negative zero.
 *
 * `-0` passes every numeric check -- it is zero, it is finite, it is an integer
 * -- and is refused anyway: it is the shape of a number that has been negated,
 * which is a computation's residue rather than a time.
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

/** An id that can be a database key: non-empty, bounded, no key character, no lone surrogate. */
function isKeyId(value) {
  return typeof value === 'string' && keyIdField(value, 'id').bad === undefined;
}

// keyIdField validates an id: a non-empty string that is a Realtime Database
// key (P0.2's rule), so the id can be stored under its own node.
function keyIdField(value, field) {
  // A field that is absent and a field that is present and undefined are the
  // same failure here: the event did not carry the id it must carry.
  if (value === undefined) return { bad: refuse(REFUSAL.MISSING_FIELD, field) };
  if (typeof value !== 'string') return { bad: refuse(REFUSAL.WRONG_TYPE, field) };
  if (value === '') return { bad: refuse(REFUSAL.EMPTY_STRING, field) };
  if (value.length > ID_MAX) return { bad: refuse(REFUSAL.TOO_LONG, field) };
  for (let i = 0; i < value.length; i++) {
    const code = value.charCodeAt(i);
    if (FORBIDDEN_KEY_CHARS.has(value[i]) || code < 0x20 || code === 0x7f) {
      return { bad: refuse(REFUSAL.BAD_KEY, field) };
    }
  }
  if (hasLoneSurrogate(value)) return { bad: refuse(REFUSAL.BAD_KEY, field) };
  return { value };
}

// stringField validates a required, bounded, single-line string.
function stringField(value, field, max) {
  // Absent and present-but-undefined are the same failure: the event did not
  // carry the text it must carry.
  if (value === undefined) return { bad: refuse(REFUSAL.MISSING_FIELD, field) };
  if (typeof value !== 'string') return { bad: refuse(REFUSAL.WRONG_TYPE, field) };
  if (value === '') return { bad: refuse(REFUSAL.EMPTY_STRING, field) };
  if (value.length > max) return { bad: refuse(REFUSAL.TOO_LONG, field) };
  for (let i = 0; i < value.length; i++) {
    const code = value.charCodeAt(i);
    if (code < 0x20 || code === 0x7f) return { bad: refuse(REFUSAL.BAD_CHARACTER, field) };
  }
  if (hasLoneSurrogate(value)) return { bad: refuse(REFUSAL.BAD_CHARACTER, field) };
  // An invisible format character (zero-width space, joiner, bidi control, BOM)
  // is refused as P6.1 refuses it: what is shown must be what is judged, or
  // "con\u200bfirm" reads as "confirm" on a screen and as something else here.
  if (FORMAT_CHARACTER.test(value)) return { bad: refuse(REFUSAL.BAD_CHARACTER, field) };
  return { value };
}

const FORMAT_CHARACTER = /\p{Cf}/u;

// hasLoneSurrogate reports whether s contains an unpaired surrogate: half of a
// character that JSON cannot encode, so a value carrying one cannot be written.
function hasLoneSurrogate(s) {
  for (let i = 0; i < s.length; i++) {
    const code = s.charCodeAt(i);
    if (code >= 0xd800 && code <= 0xdbff) {
      const next = s.charCodeAt(i + 1);
      if (!(next >= 0xdc00 && next <= 0xdfff)) return true;
      i++;
      continue;
    }
    if (code >= 0xdc00 && code <= 0xdfff) return true;
  }
  return false;
}

// ---------------------------------------------------------------------------
// The answers
// ---------------------------------------------------------------------------

/** A refusal: no state, a code the caller can branch on, and the field at fault. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}
