// The spec-shaping turn and the "go" (mobile plan, Task P5.4.4).
//
// One pure function of a conversation and the utterance that just arrived:
//
//     turn({ transcript, reply, now, options })
//        -> { ok: true, outcome, phase, target, brief, mode, readBack,
//             deadline, costly, biometric, transcript, action? }
//        |  { ok: false, code, field }
//
// and a read-only view of a conversation already stored: `conversation(...)`.
//
// **What it is for.** The front agent (P5.4.1, `docs/mobile/design/front-agent.md`)
// holds a conversation whose only job is to shape one **brief** -- a file whose
// first line is `# Task: <title>` -- and then to take the person's **go**: the
// go is the authorisation for the single launch the brief describes, and this
// module is the decision procedure for both halves. The launch it authorises is
// the typed O10 action record (`shared/mailbox-schema`), carrying the mode, the
// repository and the confirmation -- never a record built by the speaker.
//
// **The transcript is the state, and the ledger.** P6.2 keeps a list of
// outstanding read-backs and a ledger of spent ids, because its state outlives
// the utterance and is swept by a clock. Here there is nothing to sweep: the
// conversation *is* the transcript, every call is handed the whole of it, and a
// go that was spent is a turn that is still in it. So a replayed go needs no
// ledger to catch it -- the phase it left behind (`spent`) refuses the second
// one, and a repeated turn id is refused while the transcript is read. A module
// that stored a second copy of what the transcript already holds would be a
// second answer to the same question.
//
// **The go reuses P6.2's rules rather than writing a second set.** That reuse is
// the load-bearing claim here, and it is four imported things, not a
// re-implementation:
//
//   * the **phrase** is P6.1's closed set of one (`confirm`) as P6.2 recognises
//     it, through `isConfirmation` on a routed utterance -- so "Fugaro, confirm"
//     and "Figaro, confirm." are the go, and a bare "confirm" is agent text
//     (D164 forwards it, and a go that the agent also received is not a
//     confirmation);
//   * `carriesConfirmToken`, P6.2's rule that an utterance carrying a command
//     *and* a confirm token is refused (`command-and-go`), restated here because
//     P6.2 keeps it private -- the same misrecognition must not satisfy both
//     halves of D47's Rule;
//   * **`CONFIRM_CUE`**, the sentence that asks for the phrase, so the words the
//     phone speaks and the words the recogniser accepts cannot drift apart;
//   * **`costlyStart`** and the session's two policy values (D2786): what the
//     start costs is the session policy's answer, never this module's.
//
// What this module does **not** reuse is P6.2's machine, and the reason is worth
// stating rather than leaving to be discovered: P6.2 reads back only the
// destructive verbs and lets a launch pass through (D162), because a launch
// needs no confirmation at all. The front agent's launch does need one -- the
// brief is the thing authorised, and it is not a value a router resolved out of
// the utterance -- so the read-back names the brief and the repository as well
// as the mode, and the machine that holds it is this one. The *rules* are
// P6.2's; the sentence is this module's, because the thing read back is not the
// same thing.
//
// **The four phases, and what each one says.**
//
//     idle      nothing has been said yet
//     shaping   the conversation holds no brief, or no repository
//     ready     a brief and a repository are both held, and the go is live
//     spent     the go was taken: the conversation has authorised its one launch
//
// A phase is a summary of the transcript, derived, never stored: a go is judged
// against the phase of the conversation **before** it, so an utterance cannot
// make its own authorisation. The one thing derived here that a phase cannot
// say is *when* the conversation started, which is its first turn's `at`: the
// deadline (`startedAt + ttlMs`) is what the go is stale against.
//
// **Time is injected.** `now` is the caller's clock and the module reads no
// clock of its own: a turn stamped after `now` is refused (`invalid-now`), and a
// go past the deadline is refused (`go-expired`) -- at the deadline it is still
// live, one millisecond later it is not. The TTL is P6.5's mailbox TTL
// (`PROMPT_TTL_MS`, 72 hours, O5), not a second number: a brief that has sat
// unanswered for longer than a prompt would have is stale for the same reason.
//
// **Fail closed.** Everything that is not this conversation's input has a code
// of its own: an utterance carrying a wake word and nothing this grammar knows
// is refused rather than handed to the agent (`not-understood`, P6.1's own
// fail-closed direction), an app command is not this module's to read
// (`not-agent-text`), a brief whose first line is not a `# Task:` line is
// refused (`bad-brief`) rather than launched as a run with no title -- the one
// thing every run must have. A refusal carries no transcript, so a refused
// utterance spends nothing.
//
// Nothing here reads a clock, a file, a database, a network or a random source,
// and nothing here mutates what it is handed. The recogniser and the grammar are
// P6.1's, the read-back policy is P6.2's, the action record's shape is P6.3's,
// the TTL is P6.5's, and the Go consumer that re-validates the record and starts
// the run is P6.7's and P5.4.5's. `shared/README.md` is the package's contract
// and `../../docs/mobile/implementation-plan.md`'s P5.4.4 is this module's task.

import { CONFIRMATION_PHRASES, ROUTE, isConfirmation, routeUtterance } from '../command/index.js';
import {
  CONFIRM_CUE,
  DEFAULT_BIOMETRIC_ON_COSTLY,
  DEFAULT_COSTLY_MODES,
  MAX_COSTLY_MODES,
  MAX_MODE_LENGTH,
  costlyStart,
} from '../confirm/index.js';
import { MAX_TIMESTAMP } from '../lease/index.js';
import { ID_MAX as MESSAGE_ID_MAX, TARGET_MAX, TEXT_MAX as MESSAGE_TEXT_MAX } from '../mailbox-schema/index.js';
import { MAX_TTL_MS as MAILBOX_MAX_TTL_MS, MIN_TTL_MS as MAILBOX_MIN_TTL_MS, PROMPT_TTL_MS } from '../prompts/index.js';

// The session policy the go is read against (D2786) is P6.2's, re-exported
// because this module's options carry its two values and a screen that reads a
// session's policy must not learn a second default.
export { DEFAULT_BIOMETRIC_ON_COSTLY, DEFAULT_COSTLY_MODES, CONFIRM_CUE, costlyStart } from '../confirm/index.js';

/** The two roles a turn may have. Nothing else is a role. */
export const ROLE = Object.freeze({
  /** Something the person said. */
  PERSON: 'person',
  /** The agent's answer to the person's turn before it. */
  AGENT: 'agent',
});

/**
 * The two events a newest utterance can be. A third would be a decision.
 *
 * The event is not passed in: it is read off the utterance by P6.1's router,
 * because "is this a go" is the one question a caller must not be able to
 * answer for itself.
 */
export const EVENT = Object.freeze({
  /** Agent text: the agent answers it, and the answer becomes the next turn. */
  TURN: 'turn',
  /** P6.1's `confirm` app command: the conversation's one authorisation. */
  GO: 'go',
});

/**
 * The four phases. A phase is derived from the transcript, so there is no state
 * to disagree with itself -- this is what the caller branches on.
 */
export const PHASE = Object.freeze({
  IDLE: 'idle',
  SHAPING: 'shaping',
  READY: 'ready',
  SPENT: 'spent',
});

/** What a `turn` call answers with. */
export const OUTCOME = Object.freeze({
  /** The conversation was shaped, or read back: nothing was authorised. */
  SHAPING: 'shaping',
  /** The go was taken, and it authorised exactly one launch. */
  GO: 'go',
});

/** The verb the authorised action carries: D162's launch, typed by O10. */
export const VERB = Object.freeze({ LAUNCH: 'launch' });

/**
 * The permitted length of a turn, reply, action or confirmation id: P6.3's
 * ID_MAX, imported. This module stores nothing, but every id it accepts is one
 * P6.3's action record must hold, so the two must not disagree.
 */
export const ID_MAX = MESSAGE_ID_MAX;

/** The permitted length of an utterance, a reply or a brief (P0.2's TEXT_MAX, imported). */
export const TEXT_MAX = MESSAGE_TEXT_MAX;

/** The permitted length of a repository: P6.3's TARGET_MAX, which the action record must hold. */
export const REPO_MAX = TARGET_MAX;

/** The permitted length of a rigor mode (P6.2's policy bound: a mode is a short word). */
export const MODE_MAX = MAX_MODE_LENGTH;

/** The latest `now` or `at` this module reads (shared/lease's MAX_TIMESTAMP: two modules must not disagree about a time). */
export const TIME_MAX = MAX_TIMESTAMP;

/**
 * How long a shaped brief stays live, in milliseconds: **P6.5's mailbox TTL**,
 * 72 hours (the owner's answer of 2026-10-17, O5), imported rather than restated.
 * It is this module's default and not a number it imposes: `ttlMs` is an option
 * and an operator who moves the sweeper's window moves this with it.
 */
export const DEFAULT_TTL_MS = PROMPT_TTL_MS;

/** The TTL's bounds: P1.1's floor and ceiling, through P6.5, so the two cannot disagree. */
export const MIN_TTL_MS = MAILBOX_MIN_TTL_MS;
export const MAX_TTL_MS = MAILBOX_MAX_TTL_MS;

/** How many turns a conversation may hold. A bound, not a policy: past it the transcript is a log nobody is reading. */
export const DEFAULT_MAX_TURNS = 64;
export const MIN_MAX_TURNS = 1;
export const MAX_MAX_TURNS = 256;

/** The brief's bound, and P0.2's TEXT_MAX is the ceiling: the brief is written into a task file, and that is what a task may hold. */
export const DEFAULT_MAX_BRIEF = MESSAGE_TEXT_MAX;
export const MIN_MAX_BRIEF = 1;
export const MAX_MAX_BRIEF = TEXT_MAX;

/**
 * The first line every brief must have. `fugaro watch` and the pull request
 * title come from it, and a launch with no title is refused at the far end, so
 * it is refused here instead: a brief that cannot name its run never leaves the
 * conversation.
 */
const TASK_MARKER = '# Task: ';

/**
 * Every way an input can fail to be this module's input has its own code, and a
 * test sweeps the module for codes this table and `REFUSAL` disagree about. A
 * refusal is `{ ok: false, code, field }`, frozen, carrying **no transcript** --
 * so a caller cannot render a refused utterance as an answered one -- and
 * `field` is a path this module chose, never a key or a value the input chose.
 */
export const REFUSAL = Object.freeze({
  /** The value is not a plain object: null, an array, a primitive, an instance. */
  NOT_AN_OBJECT: 'not-an-object',
  /** A field this module does not name is present. */
  UNKNOWN_FIELD: 'unknown-field',
  /** A field this turn's kind does not carry, or a reply where none is answered. */
  UNEXPECTED_FIELD: 'unexpected-field',
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
  /** A string contains a control character, a lone surrogate, an invisible format character or a line separator. */
  BAD_CHARACTER: 'bad-character',
  /** A `now` or an `at` that is not an epoch-millisecond integer in 1 .. TIME_MAX, or an `at` after `now`. */
  INVALID_NOW: 'invalid-now',
  /** A `role` that is neither of the two. */
  UNKNOWN_ROLE: 'unknown-role',
  /** A `transcript` that is not a plain array, or whose first turn is the agent's. */
  INVALID_TRANSCRIPT: 'invalid-transcript',
  /** A transcript of no turns: there is nothing to judge. */
  EMPTY_TRANSCRIPT: 'empty-transcript',
  /** A transcript longer than `maxTurns`. */
  TOO_MANY_TURNS: 'too-many-turns',
  /** A turn id that appears twice: one utterance, one turn. */
  REPLAYED_TURN: 'replayed-turn',
  /** A turn stamped before the turn before it. */
  UNSORTED_TRANSCRIPT: 'unsorted-transcript',
  /** A `ttlMs` outside MIN_TTL_MS .. MAX_TTL_MS. */
  INVALID_TIMEOUT: 'invalid-timeout',
  /** A `maxTurns` outside MIN_MAX_TURNS .. MAX_MAX_TURNS. */
  INVALID_MAX_TURNS: 'invalid-max-turns',
  /** A `maxBrief` outside MIN_MAX_BRIEF .. MAX_MAX_BRIEF. */
  INVALID_MAX_BRIEF: 'invalid-max-brief',
  /** A `mode` that is not a bounded, non-empty mode name. */
  INVALID_MODE: 'invalid-mode',
  /** A `costlyModes` that is not a bounded list of mode names. */
  INVALID_COSTLY_MODES: 'invalid-costly-modes',
  /** A target that is not `owner/name`. */
  BAD_REPO: 'bad-repo',
  /** A brief whose first line is not `# Task: <title>`. */
  BAD_BRIEF: 'bad-brief',
  /** An app command: not the agent's to read. */
  NOT_AGENT_TEXT: 'not-agent-text',
  /** A wake-word utterance the grammar does not know: P6.1 never forwards it. */
  NOT_UNDERSTOOD: 'not-understood',
  /** One utterance carrying a command and a confirm token. */
  COMMAND_AND_GO: 'command-and-go',
  /** A person turn with no reply answered yet. */
  MISSING_REPLY: 'missing-reply',
  /** A reply beside a go, or beside the agent's own turn. */
  UNEXPECTED_REPLY: 'unexpected-reply',
  /** A turn, or a go, in a conversation that has taken its go. */
  CONVERSATION_CLOSED: 'conversation-closed',
  /** A go in a conversation that already took one. */
  REPLAYED_GO: 'replayed-go',
  /** A go with no resolved repository: nothing to launch in. */
  UNRESOLVED_TARGET: 'unresolved-target',
  /** A go with no brief shaped: nothing to launch. */
  NO_BRIEF: 'no-brief',
  /** A go past the conversation's deadline. */
  GO_EXPIRED: 'go-expired',
});

/**
 * The refusals that are about the *input* rather than about the answer: a
 * transcript that is not a transcript, a field of the wrong type, a `now`
 * outside its window. They belong to no cell, because they are refused before
 * the conversation's phase can matter -- which is why the test can require every
 * other code to be declared in a cell and reached by a probe, and still account
 * for these.
 */
export const INPUT_REFUSALS = Object.freeze([
  REFUSAL.ACCESSOR_PROPERTY,
  REFUSAL.BAD_BRIEF,
  REFUSAL.BAD_CHARACTER,
  REFUSAL.BAD_KEY,
  REFUSAL.BAD_REPO,
  REFUSAL.EMPTY_STRING,
  REFUSAL.EMPTY_TRANSCRIPT,
  REFUSAL.INTERNAL_ERROR,
  REFUSAL.INVALID_COSTLY_MODES,
  REFUSAL.INVALID_MAX_BRIEF,
  REFUSAL.INVALID_MAX_TURNS,
  REFUSAL.INVALID_MODE,
  REFUSAL.INVALID_NOW,
  REFUSAL.INVALID_TIMEOUT,
  REFUSAL.INVALID_TRANSCRIPT,
  REFUSAL.MISSING_FIELD,
  REFUSAL.NOT_AN_OBJECT,
  REFUSAL.REPLAYED_TURN,
  REFUSAL.TOO_LONG,
  REFUSAL.TOO_MANY_TURNS,
  REFUSAL.UNEXPECTED_FIELD,
  REFUSAL.UNKNOWN_FIELD,
  REFUSAL.UNKNOWN_ROLE,
  REFUSAL.UNSORTED_TRANSCRIPT,
  REFUSAL.WRONG_TYPE,
]);

/** A cell of the transition table: the outcomes, the phases and the refusals it may answer with. */
function cell(ok, next, no) {
  return Object.freeze({
    ok: Object.freeze(ok),
    next: Object.freeze(next),
    no: Object.freeze(no),
  });
}

/**
 * The transition table, as data: every phase x every event, with the outcomes it
 * may answer with, the phases it may leave the conversation in, and the
 * conversational refusals it may answer with (the input refusals above belong to
 * no cell). A `next` list is a summary: shaping a turn can leave the
 * conversation `shaping` or make it `ready`, and a refused go leaves it where it
 * was. The test writes the expected table **by hand** -- never importing this
 * one -- asserts the two agree, and then drives a literal transcript through
 * every cell.
 */
export const TRANSITIONS = Object.freeze({
  [PHASE.IDLE]: Object.freeze({
    [EVENT.TURN]: cell(
      [OUTCOME.SHAPING],
      [PHASE.SHAPING, PHASE.READY],
      [REFUSAL.NOT_AGENT_TEXT, REFUSAL.NOT_UNDERSTOOD, REFUSAL.COMMAND_AND_GO, REFUSAL.MISSING_REPLY],
    ),
    [EVENT.GO]: cell(
      [],
      [PHASE.IDLE],
      [REFUSAL.NOT_AGENT_TEXT, REFUSAL.NOT_UNDERSTOOD, REFUSAL.COMMAND_AND_GO, REFUSAL.UNEXPECTED_REPLY, REFUSAL.UNRESOLVED_TARGET],
    ),
  }),
  [PHASE.SHAPING]: Object.freeze({
    [EVENT.TURN]: cell(
      [OUTCOME.SHAPING],
      [PHASE.SHAPING, PHASE.READY],
      [REFUSAL.NOT_AGENT_TEXT, REFUSAL.NOT_UNDERSTOOD, REFUSAL.COMMAND_AND_GO, REFUSAL.MISSING_REPLY],
    ),
    [EVENT.GO]: cell(
      [],
      [PHASE.SHAPING],
      [
        REFUSAL.NOT_AGENT_TEXT,
        REFUSAL.NOT_UNDERSTOOD,
        REFUSAL.COMMAND_AND_GO,
        REFUSAL.UNEXPECTED_REPLY,
        REFUSAL.UNRESOLVED_TARGET,
        REFUSAL.NO_BRIEF,
        REFUSAL.MISSING_FIELD,
        REFUSAL.GO_EXPIRED,
      ],
    ),
  }),
  [PHASE.READY]: Object.freeze({
    [EVENT.TURN]: cell(
      [OUTCOME.SHAPING],
      [PHASE.SHAPING, PHASE.READY],
      [REFUSAL.NOT_AGENT_TEXT, REFUSAL.NOT_UNDERSTOOD, REFUSAL.COMMAND_AND_GO, REFUSAL.MISSING_REPLY],
    ),
    [EVENT.GO]: cell(
      [OUTCOME.GO],
      [PHASE.SPENT],
      [REFUSAL.NOT_AGENT_TEXT, REFUSAL.NOT_UNDERSTOOD, REFUSAL.COMMAND_AND_GO, REFUSAL.UNEXPECTED_REPLY, REFUSAL.MISSING_FIELD, REFUSAL.GO_EXPIRED],
    ),
  }),
  [PHASE.SPENT]: Object.freeze({
    [EVENT.TURN]: cell(
      [],
      [PHASE.SPENT],
      [REFUSAL.NOT_AGENT_TEXT, REFUSAL.NOT_UNDERSTOOD, REFUSAL.COMMAND_AND_GO, REFUSAL.MISSING_REPLY, REFUSAL.CONVERSATION_CLOSED],
    ),
    [EVENT.GO]: cell(
      [],
      [PHASE.SPENT],
      [REFUSAL.NOT_AGENT_TEXT, REFUSAL.NOT_UNDERSTOOD, REFUSAL.COMMAND_AND_GO, REFUSAL.UNEXPECTED_REPLY, REFUSAL.REPLAYED_GO],
    ),
  }),
});

/** The exact fields a call may carry. */
const TURN_FIELDS = Object.freeze(['transcript', 'reply', 'now', 'options']);
const CONVERSATION_FIELDS = Object.freeze(['transcript', 'now', 'options']);
const OPTION_FIELDS = Object.freeze(['ttlMs', 'maxTurns', 'maxBrief', 'mode', 'costlyModes', 'biometricOnCostly']);
const RECORD_FIELDS = Object.freeze(['id', 'role', 'text', 'at', 'target', 'brief', 'actionId', 'confirmationId']);
const REPLY_FIELDS = Object.freeze(['id', 'text', 'brief']);
const TARGET_FIELDS = Object.freeze(['repo']);

/** Characters a Realtime Database key may not contain (P0.2's rule, restated here because this module validates its own ids). */
const FORBIDDEN_KEY_CHARS = new Set(['%', '.', '$', '#', '[', ']', '/']);

const FORMAT_CHARACTER = /\p{Cf}/u;
const REPO_SEGMENT_PATTERN = /^[A-Za-z0-9_][A-Za-z0-9._-]*$/;

// U+2028 LINE SEPARATOR and U+2029 PARAGRAPH SEPARATOR are refused where P6.3
// refuses them (`mailbox-schema`'s own `isSeparator`): Go's `encoding/json`
// escapes both and JavaScript's `JSON.stringify` leaves them, so a value
// carrying one has two byte strings across the two ends. This module's reason
// is the sharper one -- the title is interpolated verbatim into the read-back,
// the single sentence the person is meant to trust, where either character is a
// line break inside it.
function isSeparator(code) {
  return code === 0x2028 || code === 0x2029;
}

/**
 * turn answers what one utterance does to a conversation.
 *
 * @param {object} input `{ transcript, reply = null, now, options = {} }`
 * @returns {object} the answer, frozen: an authorisation, a shaped turn, or a refusal
 */
export function turn(input) {
  try {
    const read = readCall(input, TURN_FIELDS);
    if (!read.ok) return read;
    const { transcript, reply, now, options } = read.values;
    const newest = transcript[transcript.length - 1];
    if (newest === undefined) return refuse(REFUSAL.EMPTY_TRANSCRIPT, 'transcript');

    // The phase the conversation stood in **before** the newest utterance: a go
    // cannot make its own authorisation, and a turn cannot answer its own reply.
    const before = derive(transcript.slice(0, -1), options);

    if (newest.role === ROLE.AGENT) {
      // The reply is already in the transcript, so there is nothing to judge:
      // this call is a reading of where the conversation stands.
      if (reply !== null) return refuse(REFUSAL.UNEXPECTED_REPLY, 'reply');
      const whole = derive(transcript, options);
      if (whole.spent) return refuse(REFUSAL.CONVERSATION_CLOSED, null);
      return answer(whole, transcript, options, null);
    }

    if (isGo(newest)) {
      if (reply !== null) return refuse(REFUSAL.UNEXPECTED_REPLY, 'reply');
      if (before.spent) return refuse(REFUSAL.REPLAYED_GO, null);
      if (before.target === null) return refuse(REFUSAL.UNRESOLVED_TARGET, 'target');
      if (before.brief === null) return refuse(REFUSAL.NO_BRIEF, 'brief');
      if (options.mode === null) return refuse(REFUSAL.MISSING_FIELD, 'mode');
      if (before.deadline !== null && now > before.deadline) return refuse(REFUSAL.GO_EXPIRED, null);
      return authorised(newest, before, transcript, options);
    }

    if (reply === null) return refuse(REFUSAL.MISSING_REPLY, 'reply');
    if (before.spent) return refuse(REFUSAL.CONVERSATION_CLOSED, null);
    const next = Object.freeze([...transcript, reply]);
    return answer(derive(next, options), next, options, null);
  } catch {
    return refuse(REFUSAL.INTERNAL_ERROR, null);
  }
}

/**
 * conversation reads a stored conversation without judging an utterance: the
 * phase, the brief, the repository and the read-back a screen draws.
 *
 * @param {object} input `{ transcript, now, options = {} }`
 * @returns {object} the view, frozen, or a refusal
 */
export function conversation(input) {
  try {
    const read = readCall(input, CONVERSATION_FIELDS);
    if (!read.ok) return read;
    const { transcript, now, options } = read.values;
    const derived = derive(transcript, options);
    return Object.freeze({
      ok: true,
      phase: derived.phase,
      target: derived.target,
      brief: derived.brief,
      mode: options.mode,
      readBack: derived.phase === PHASE.READY ? readBack(derived.target, derived.brief, options.mode) : null,
      deadline: derived.deadline,
      ttlMs: options.ttlMs,
      expired: derived.deadline !== null && now > derived.deadline,
    });
  } catch {
    return refuse(REFUSAL.INTERNAL_ERROR, null);
  }
}

/**
 * readBack is the sentence a screen speaks before the go: what would be launched,
 * where, and in which mode, then P6.2's cue -- so the sentence that asks for the
 * phrase and the recogniser that accepts it are the same rule's.
 *
 * It is a function of the resolved values and **recomputed, never stored** (P6.2's
 * rule for its own read-backs): a view handed back with an edited read-back
 * cannot be spoken. It answers null for anything it cannot read, which is why it
 * never throws.
 *
 * @param {unknown} target the resolved `{ repo }`
 * @param {unknown} brief the shaped brief
 * @param {unknown} mode the mode the launch would run in
 * @returns {string | null} the read-back to speak, or null
 */
export function readBack(target, brief, mode) {
  if (!isPlainObject(target) || typeof target.repo !== 'string') return null;
  if (typeof brief !== 'string' || !brief.startsWith(TASK_MARKER)) return null;
  if (typeof mode !== 'string' || mode === '') return null;
  const title = firstLine(brief).slice(TASK_MARKER.length);
  return `Start "${title}" in ${target.repo} (${mode} mode). ${CONFIRM_CUE}`;
}

// ---------------------------------------------------------------------------
// The answers
// ---------------------------------------------------------------------------

// authorised builds the one thing a go produces: O10's typed launch, whose
// target is the repository that was read back and whose mode is the session's,
// plus the confirmation that authorises it. Nothing here is parsed out of the
// utterance -- the go supplies the two ids and its own time, and nothing else.
function authorised(go, before, transcript, options) {
  const derived = derive(transcript, options);
  const policy = costlyStart(options.mode, options.costlyModes, options.biometricOnCostly);
  return Object.freeze({
    ok: true,
    outcome: OUTCOME.GO,
    phase: PHASE.SPENT,
    target: derived.target,
    brief: before.brief,
    mode: options.mode,
    readBack: null,
    deadline: derived.deadline,
    costly: policy.costly,
    biometric: policy.biometric,
    transcript,
    action: Object.freeze({
      id: go.actionId,
      verb: VERB.LAUNCH,
      mode: options.mode,
      target: Object.freeze({ repo: before.target.repo }),
      confirmation: Object.freeze({ id: go.confirmationId, at: go.at }),
    }),
  });
}

function answer(derived, transcript, options, action) {
  const policy = costlyStart(options.mode, options.costlyModes, options.biometricOnCostly);
  return Object.freeze({
    ok: true,
    outcome: action === null ? OUTCOME.SHAPING : OUTCOME.GO,
    phase: derived.phase,
    target: derived.target,
    brief: derived.brief,
    mode: options.mode,
    readBack: derived.phase === PHASE.READY ? readBack(derived.target, derived.brief, options.mode) : null,
    deadline: derived.deadline,
    costly: policy.costly,
    biometric: policy.biometric,
    transcript,
    action,
  });
}

/**
 * isGo answers whether a record is the go, out of the record's own shape: the
 * reader lets a go carry an action id and a confirmation id and lets nothing
 * else carry either, so the presence of the action id *is* the event -- which is
 * why a record needs no field of its own to say what it is. (A transcript read
 * back from a stored conversation is therefore read the same way as one just
 * built, and a hand-edited record cannot claim to be a go without the ids the
 * go's authorisation needs.)
 */
function isGo(record) {
  return record.role === ROLE.PERSON && record.actionId !== undefined;
}

/**
 * derive reads the conversation out of a transcript. It is the whole state: the
 * repository is the last turn that named one (a turn that named none keeps the
 * one before it, and a turn that resolved none clears it), the brief is the
 * agent's latest answer, and a conversation that holds a go has spent its one
 * launch.
 */
function derive(turns, options) {
  let target = null;
  let brief = null;
  let startedAt = null;
  for (const one of turns) {
    if (startedAt === null) startedAt = one.at;
    if (one.role === ROLE.PERSON) {
      if (!isGo(one) && one.target !== undefined) target = one.target;
    } else {
      brief = one.brief;
    }
  }
  const spent = turns.some(isGo);
  const deadline = startedAt === null ? null : startedAt + options.ttlMs;
  let phase = PHASE.IDLE;
  if (spent) phase = PHASE.SPENT;
  else if (startedAt !== null) phase = target !== null && brief !== null ? PHASE.READY : PHASE.SHAPING;
  return { phase, target, brief, deadline, spent, startedAt };
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

function readCall(input, names) {
  const read = readFields(input, names, null);
  if (!read.ok) return read;
  const values = read.values;
  if (!isIntegerInRange(values.now, 1, TIME_MAX)) return refuse(REFUSAL.INVALID_NOW, 'now');
  const options = readOptions(values.options);
  if (!options.ok) return options;
  const transcript = readTranscript(values.transcript, values.now, options.values);
  if (!transcript.ok) return transcript;
  let reply = null;
  if (names === TURN_FIELDS && values.reply !== undefined && values.reply !== null) {
    const readReply = readReplyRecord(values.reply, values.now, options.values.maxBrief);
    if (!readReply.ok) return readReply;
    reply = readReply.values;
  }
  return {
    ok: true,
    values: Object.freeze({
      transcript: transcript.values,
      reply,
      now: values.now,
      options: options.values,
    }),
  };
}

function readOptions(value) {
  const defaults = {
    ttlMs: DEFAULT_TTL_MS,
    maxTurns: DEFAULT_MAX_TURNS,
    maxBrief: DEFAULT_MAX_BRIEF,
    mode: null,
    costlyModes: DEFAULT_COSTLY_MODES,
    biometricOnCostly: DEFAULT_BIOMETRIC_ON_COSTLY,
  };
  if (value === undefined || value === null) return { ok: true, values: Object.freeze(defaults) };
  const read = readFields(value, OPTION_FIELDS, 'options');
  if (!read.ok) return read;
  const values = read.values;
  const ttlMs = values.ttlMs === undefined ? defaults.ttlMs : values.ttlMs;
  if (!isIntegerInRange(ttlMs, MIN_TTL_MS, MAX_TTL_MS)) return refuse(REFUSAL.INVALID_TIMEOUT, 'options.ttlMs');
  const maxTurns = values.maxTurns === undefined ? defaults.maxTurns : values.maxTurns;
  if (!isIntegerInRange(maxTurns, MIN_MAX_TURNS, MAX_MAX_TURNS)) return refuse(REFUSAL.INVALID_MAX_TURNS, 'options.maxTurns');
  const maxBrief = values.maxBrief === undefined ? defaults.maxBrief : values.maxBrief;
  if (!isIntegerInRange(maxBrief, MIN_MAX_BRIEF, MAX_MAX_BRIEF)) return refuse(REFUSAL.INVALID_MAX_BRIEF, 'options.maxBrief');
  let mode = defaults.mode;
  if (values.mode !== undefined) {
    if (typeof values.mode !== 'string' || values.mode === '' || values.mode.length > MODE_MAX || hasBadCharacter(values.mode)) {
      return refuse(REFUSAL.INVALID_MODE, 'options.mode');
    }
    mode = values.mode;
  }
  let costlyModes = defaults.costlyModes;
  if (values.costlyModes !== undefined) {
    const list = readModeList(values.costlyModes);
    if (list === null) return refuse(REFUSAL.INVALID_COSTLY_MODES, 'options.costlyModes');
    costlyModes = list;
  }
  let biometricOnCostly = defaults.biometricOnCostly;
  if (values.biometricOnCostly !== undefined) {
    if (typeof values.biometricOnCostly !== 'boolean') return refuse(REFUSAL.WRONG_TYPE, 'options.biometricOnCostly');
    biometricOnCostly = values.biometricOnCostly;
  }
  return { ok: true, values: Object.freeze({ ttlMs, maxTurns, maxBrief, mode, costlyModes, biometricOnCostly }) };
}

// readModeList reads the session's costly-mode list, or answers null. A list
// that is not a list of bounded mode names is refused rather than filtered: a
// policy that silently loses a member is not the policy the session declared.
function readModeList(value) {
  if (!Array.isArray(value) || Object.getPrototypeOf(value) !== Array.prototype) return null;
  if (value.length > MAX_COSTLY_MODES) return null;
  const modes = [];
  for (let index = 0; index < value.length; index += 1) {
    const descriptor = Object.getOwnPropertyDescriptor(value, String(index));
    if (descriptor === undefined || !Object.hasOwn(descriptor, 'value')) return null;
    const one = descriptor.value;
    if (typeof one !== 'string' || one === '' || one.length > MODE_MAX || hasBadCharacter(one)) return null;
    if (modes.includes(one)) return null;
    modes.push(one);
  }
  return Object.freeze(modes);
}

function readTranscript(value, now, options) {
  if (!Array.isArray(value) || Object.getPrototypeOf(value) !== Array.prototype) {
    return refuse(REFUSAL.INVALID_TRANSCRIPT, 'transcript');
  }
  const length = Object.getOwnPropertyDescriptor(value, 'length').value;
  if (length > options.maxTurns) return refuse(REFUSAL.TOO_MANY_TURNS, 'transcript');
  if (Reflect.ownKeys(value).length !== length + 1) return refuse(REFUSAL.INVALID_TRANSCRIPT, 'transcript');
  const turns = [];
  const ids = new Set();
  let lastAt = 0;
  for (let index = 0; index < length; index += 1) {
    const descriptor = Object.getOwnPropertyDescriptor(value, String(index));
    if (descriptor === undefined || !Object.hasOwn(descriptor, 'value')) return refuse(REFUSAL.INVALID_TRANSCRIPT, 'transcript');
    const one = readTurnRecord(descriptor.value, index, now, options);
    if (!one.ok) return one;
    const turn = one.values;
    if (ids.has(turn.id)) return refuse(REFUSAL.REPLAYED_TURN, `transcript[${index}].id`);
    if (turn.at < lastAt) return refuse(REFUSAL.UNSORTED_TRANSCRIPT, `transcript[${index}].at`);
    ids.add(turn.id);
    lastAt = turn.at;
    turns.push(turn);
  }
  // An agent reply with nothing to reply to is not a conversation: the brief
  // would be an answer to nobody.
  if (turns.length > 0 && turns[0].role === ROLE.AGENT) return refuse(REFUSAL.INVALID_TRANSCRIPT, 'transcript[0].role');
  return { ok: true, values: Object.freeze(turns) };
}

function readTurnRecord(value, index, now, options) {
  const where = `transcript[${index}]`;
  const read = readFields(value, RECORD_FIELDS, where);
  if (!read.ok) return read;
  const values = read.values;
  if (values.role !== ROLE.PERSON && values.role !== ROLE.AGENT) return refuse(REFUSAL.UNKNOWN_ROLE, `${where}.role`);
  const id = keyIdField(values.id, `${where}.id`);
  if (id.bad !== undefined) return id.bad;
  const at = values.at;
  if (!isIntegerInRange(at, 1, now)) return refuse(REFUSAL.INVALID_NOW, `${where}.at`);
  if (values.role === ROLE.AGENT) {
    const text = textField(values.text, `${where}.text`, TEXT_MAX, false);
    if (text.bad !== undefined) return text.bad;
    // The brief is what the agent shaped, and a reply that holds none is a reply
    // -- the conversation is shaping until a brief arrives, and shaping again
    // when a later answer withdraws it.
    let brief = null;
    if (values.brief !== undefined && values.brief !== null) {
      const readBrief = briefField(values.brief, `${where}.brief`, options.maxBrief);
      if (readBrief.bad !== undefined) return readBrief.bad;
      brief = readBrief.value;
    }
    for (const field of ['target', 'actionId', 'confirmationId']) {
      if (values[field] !== undefined) return refuse(REFUSAL.UNEXPECTED_FIELD, field);
    }
    return { ok: true, values: Object.freeze({ id: id.value, role: ROLE.AGENT, at, text: text.value, brief }) };
  }

  const text = textField(values.text, `${where}.text`, TEXT_MAX, true);
  if (text.bad !== undefined) return text.bad;
  const classified = classify(text.value);
  if (classified.code !== undefined) return refuse(classified.code, `${where}.text`);

  if (classified.kind === EVENT.GO) {
    if (values.target !== undefined) return refuse(REFUSAL.UNEXPECTED_FIELD, 'target');
    if (values.brief !== undefined) return refuse(REFUSAL.UNEXPECTED_FIELD, 'brief');
    const actionId = keyIdField(values.actionId, `${where}.actionId`);
    if (actionId.bad !== undefined) return actionId.bad;
    const confirmationId = keyIdField(values.confirmationId, `${where}.confirmationId`);
    if (confirmationId.bad !== undefined) return confirmationId.bad;
    return {
      ok: true,
      values: Object.freeze({
        id: id.value,
        role: ROLE.PERSON,
        at,
        text: text.value,
        actionId: actionId.value,
        confirmationId: confirmationId.value,
      }),
    };
  }

  for (const field of ['actionId', 'confirmationId', 'brief']) {
    if (values[field] !== undefined) return refuse(REFUSAL.UNEXPECTED_FIELD, field);
  }
  // A person turn may name a repository, clear one it could not resolve, or say
  // nothing about it -- and the three are different: no field keeps the
  // repository the conversation already holds.
  let target;
  if (values.target === null) target = null;
  else if (values.target !== undefined) {
    const readTarget = repoField(values.target);
    if (readTarget.bad !== undefined) return readTarget.bad;
    target = readTarget.value;
  }
  // A record carries only the fields it holds: the object a caller hands back in
  // is read by the same reader, so a `target: undefined` left on a turn that
  // named none would be a field the next call has to know to ignore.
  const record = { id: id.value, role: ROLE.PERSON, at, text: text.value };
  if (target !== undefined) record.target = target;
  return { ok: true, values: Object.freeze(record) };
}

function readReplyRecord(value, now, maxBrief) {
  const read = readFields(value, REPLY_FIELDS, 'reply');
  if (!read.ok) return read;
  const values = read.values;
  const id = keyIdField(values.id, 'reply.id');
  if (id.bad !== undefined) return id.bad;
  const text = textField(values.text, 'reply.text', TEXT_MAX, false);
  if (text.bad !== undefined) return text.bad;
  let brief = null;
  if (values.brief !== undefined && values.brief !== null) {
    const readBrief = briefField(values.brief, 'reply.brief', maxBrief);
    if (readBrief.bad !== undefined) return readBrief.bad;
    brief = readBrief.value;
  }
  // The reply is answering a turn that arrived before this call, so it is
  // stamped with the clock the call was judged at -- the host's, never the
  // module's.
  return { ok: true, values: Object.freeze({ id: id.value, role: ROLE.AGENT, at: now, text: text.value, brief }) };
}

/**
 * classify routes one utterance through P6.1's router and says which event it is.
 *
 * The go is P6.2's answer on a routed utterance, never a string test, and an
 * utterance the router refuses is refused here too: it could have been an app
 * command, so it is not the agent's to read.
 */
function classify(text) {
  const routed = routeUtterance({ utterance: text });
  if (!routed.ok) return { code: REFUSAL.NOT_UNDERSTOOD };
  if (isConfirmation(routed)) return { kind: EVENT.GO };
  if (routed.route === ROUTE.NOT_UNDERSTOOD) {
    return { code: carriesConfirmToken(text) ? REFUSAL.COMMAND_AND_GO : REFUSAL.NOT_UNDERSTOOD };
  }
  if (routed.route === ROUTE.APP_COMMAND) return { code: REFUSAL.NOT_AGENT_TEXT };
  return { kind: EVENT.TURN };
}

// Whether a transcript carries the confirming phrase as one of its words: P6.2's
// rule, restated because P6.2 keeps it private. Words are split on whitespace and
// on punctuation, because P6.1 strips punctuation before matching a word, so
// "Fugaro, confirm it." carries the phrase as much as "... confirm it" does.
// Splitting wider than the router only ever refuses.
function carriesConfirmToken(text) {
  return text
    .toLowerCase()
    .split(/[\s\p{P}\p{S}]+/u)
    .some((word) => CONFIRMATION_PHRASES.includes(word));
}

// ---------------------------------------------------------------------------
// The field readers
// ---------------------------------------------------------------------------

/** readFields reads a plain object's own data properties, refusing anything else. */
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

/** A plain data object, and not an instance of anything. */
function isPlainObject(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

/**
 * An integer inside a closed range, and never a negative zero: `-0` is the shape
 * of a number that has been negated, which is a computation's residue rather
 * than a time.
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

// keyIdField validates a required id: a non-empty string that is a Realtime
// Database key (P0.2's rule), so the id can be stored under its own node. P6.3's
// reader refuses the same set -- the forbidden key characters, the control
// characters, a lone surrogate and the two line separators -- because this id is
// the one an action record carries, and the two readers must not disagree.
function keyIdField(value, field) {
  if (value === undefined) return { bad: refuse(REFUSAL.MISSING_FIELD, field) };
  if (typeof value !== 'string') return { bad: refuse(REFUSAL.WRONG_TYPE, field) };
  if (value === '') return { bad: refuse(REFUSAL.EMPTY_STRING, field) };
  if (value.length > ID_MAX) return { bad: refuse(REFUSAL.TOO_LONG, field) };
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index);
    if (FORBIDDEN_KEY_CHARS.has(value[index]) || code < 0x20 || code === 0x7f || isSeparator(code)) {
      return { bad: refuse(REFUSAL.BAD_KEY, field) };
    }
  }
  if (hasLoneSurrogate(value)) return { bad: refuse(REFUSAL.BAD_KEY, field) };
  return { value };
}

// textField validates a required, bounded string. `singleLine` is what an
// utterance is -- a person says one thing, and the router reads it as words --
// while a reply may hold paragraphs.
function textField(value, field, max, singleLine) {
  if (value === undefined) return { bad: refuse(REFUSAL.MISSING_FIELD, field) };
  if (typeof value !== 'string') return { bad: refuse(REFUSAL.WRONG_TYPE, field) };
  if (value === '') return { bad: refuse(REFUSAL.EMPTY_STRING, field) };
  if (value.length > max) return { bad: refuse(REFUSAL.TOO_LONG, field) };
  if (hasBadCharacter(value, !singleLine)) return { bad: refuse(REFUSAL.BAD_CHARACTER, field) };
  return { value };
}

// briefField validates the brief: a bounded block of text whose FIRST LINE is a
// `# Task: <title>` line. The marker is the whole reason this is checked here
// rather than trusted -- `fugaro watch` and the pull-request title come from that
// line, and a run with no title is refused where it is launched.
function briefField(value, field, max) {
  if (typeof value !== 'string') return { bad: refuse(REFUSAL.WRONG_TYPE, field) };
  if (value === '') return { bad: refuse(REFUSAL.EMPTY_STRING, field) };
  if (value.length > max) return { bad: refuse(REFUSAL.TOO_LONG, field) };
  if (hasBadCharacter(value, true)) return { bad: refuse(REFUSAL.BAD_CHARACTER, field) };
  const line = firstLine(value);
  if (!line.startsWith(TASK_MARKER)) return { bad: refuse(REFUSAL.BAD_BRIEF, field) };
  if (line.slice(TASK_MARKER.length).trim() === '') return { bad: refuse(REFUSAL.BAD_BRIEF, field) };
  return { value };
}

// repoField validates the resolved target: exactly one repository, `owner/name`,
// each segment shaped so that neither the repository nor a segment can begin with
// the `-` or `.` that would make it a flag or a path to `git` or `gh`.
function repoField(value) {
  // Whatever the target is that is not a repository -- an array, a string, an
  // object with a field this module does not name -- the answer is the same one:
  // this is not a target, so there is nothing to launch in.
  const read = readFields(value, TARGET_FIELDS, 'target');
  if (!read.ok) return { bad: refuse(REFUSAL.BAD_REPO, 'target') };
  const repo = read.values.repo;
  if (typeof repo !== 'string' || repo === '' || repo.length > REPO_MAX) return { bad: refuse(REFUSAL.BAD_REPO, 'target.repo') };
  const segments = repo.split('/');
  if (segments.length !== 2) return { bad: refuse(REFUSAL.BAD_REPO, 'target.repo') };
  for (const part of segments) {
    if (part.length > 100 || !REPO_SEGMENT_PATTERN.test(part)) return { bad: refuse(REFUSAL.BAD_REPO, 'target.repo') };
  }
  return { value: Object.freeze({ repo }) };
}

function firstLine(value) {
  const end = value.indexOf('\n');
  return end === -1 ? value : value.slice(0, end);
}

// hasBadCharacter reports whether a string carries a control character (a
// newline only where the field is a block of lines), a lone surrogate, an
// invisible format character or a line separator. P6.1 refuses the format
// characters for the utterance and for the same reason: what is shown must be
// what is judged, or "con​firm" reads as "confirm" on a screen and as something
// else here. The separators are P6.3's rule (see isSeparator), and this is the
// reader a reply's text and the brief pass through.
function hasBadCharacter(value, allowsNewline) {
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index);
    if (code === 0x0a && allowsNewline) continue;
    if (code < 0x20 || code === 0x7f || isSeparator(code)) return true;
  }
  if (hasLoneSurrogate(value)) return true;
  return FORMAT_CHARACTER.test(value);
}

// hasLoneSurrogate reports whether s contains an unpaired surrogate: half of a
// character that JSON cannot encode, so a value carrying one cannot be written.
function hasLoneSurrogate(s) {
  for (let index = 0; index < s.length; index += 1) {
    const code = s.charCodeAt(index);
    if (code >= 0xd800 && code <= 0xdbff) {
      const next = s.charCodeAt(index + 1);
      if (!(next >= 0xdc00 && next <= 0xdfff)) return true;
      index += 1;
      continue;
    }
    if (code >= 0xdc00 && code <= 0xdfff) return true;
  }
  return false;
}

/** A refusal: no transcript, a code the caller can branch on, and the field at fault. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}
