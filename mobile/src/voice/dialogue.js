// The spoken confirmation and the routing in the voice screen (mobile plan,
// Task P6.12; D162, D169, D164, D168, D160, D127, D117).
//
// **What this module is.** P3.5 shipped the screen, P3.4 the audio ports and
// their fakes, P3.6 the mailbox client, and Phase 6 shipped three pure modules:
// P6.1's wake-word router, P6.2's spoken-confirmation machine and P6.5's prompt
// queue. This is the speech those three are wired into -- the spoken half of the
// path `src/text/input.js` (P6.9) built for a keyboard. It owns exactly four
// decisions, and each one is a sentence of P6.12's own verification:
//
//   1. **the routing** -- one final transcript goes through P6.1's
//      `routeUtterance`: agent text is the mailbox client's, an app command is
//      this module's, and a wake-word utterance that matched no command is
//      answered with P6.1's own sentence and **never forwarded** (D164's
//      fail-closed half);
//   2. **the spoken read-back** -- a destructive command speaks P6.2's
//      read-back, built from the resolved target, and then **waits**; the fixed
//      phrase, recognised as its own utterance, is what turns that read-back
//      into P6.3's action record;
//   3. **fail closed, at every seam** -- nothing is sent on an unconfirmed
//      transcript, an app command is never forwarded as agent text, and a
//      read-back the user did not hear is **dropped** rather than left
//      confirmable (see below, where the rule and its reason are stated);
//   4. **the prompt pause** -- P6.5's count and its wider interrupt table are
//      spoken **only at a natural pause**: while the assistant is speaking, while
//      the user is being listened to, or while a read-back is outstanding,
//      nothing is said and the questions stay queued.
//
// **The router is P6.1's and the machine is P6.2's, called the way the merged
// modules document.** No grammar is re-implemented here and no phrase is
// repaired: `isConfirmPhrase` is P6.2's answer, the phrase is recognised only as
// P6.1's `confirm` app command (so a bare "confirm" is agent text, D164), and
// every refusal a caller sees is the router's, the machine's, the client's or
// P6.5's own code, carried unchanged. This module's own codes are only for the
// facts those modules cannot know about a *dialogue*.
//
// **A destructive command is a resolved target, never a parse.** P6.1's grammar
// has no destructive rows at all, deliberately ("free text cannot reach one"),
// and nothing in the merged tree resolves a verb and a target from an utterance.
// So `propose` takes the command the app has already resolved -- the run id it
// looked up, or the repository, PR number and head it read -- exactly as P6.9's
// typed path takes it, and does the half that is the phone's: run it through the
// machine and **speak** the read-back. A target guessed from the user's own
// words would be the paraphrase the read-back is forbidden to be.
//
// **An unheard read-back is dropped, and this is the module's headline rule.**
// The read-back is the whole of D47's Rule: the speaker confirms *what they
// heard*, not what they said. So the speech is awaited, and a read-back that did
// not play to the end -- the output port refused it, or the user barged in
// mid-sentence -- is **aborted in the machine** before this call returns. The
// alternative, leaving it pending, is a read-back the speaker never heard and
// can still confirm by saying the phrase, which is precisely the "confirmation"
// D169 exists to refuse. The rule is fail-closed and it is the one the mutation
// harness proves.
//
// **A confirmation is one utterance, later, and about a target that still
// holds.** The phrase must arrive as its own utterance (the machine refuses the
// utterance that issued the command, `same-utterance`, and an utterance carrying
// both a command and the phrase, `command-and-confirm`); one confirmation
// authorises exactly one action (the action id and the confirmation id are spent
// in the machine's ledger, so a replay authorises nothing); and the timeout is
// the machine's, measured on D117's injected clock.
//
// **A merge is re-read at confirmation time, and the read-back says so.** D168's
// amendment is the reason: a prompt can be answered days later, and "approve the
// merge" answered days later approves whatever the PR then holds
// (functional-spec-reconciliation.md 3.4). So the caller may pass the target **as
// it is now** when the phrase arrives -- the head it has just re-read -- and the
// machine compares it with the one that was read back: a head that moved is
// `different-target`, nothing is authorised, and this module **re-proposes the
// command with the fresh target** so that the sentence spoken next names the head
// that is actually there. The user hears the current head and confirms again;
// nothing is ever merged against a head they did not hear.
//
// **What a confirmed action does NOT do, stated rather than left to be found.**
// The machine's `confirmed` answer carries P6.3's action record, and the record
// is **returned, not written**: `delivered: false` with `no-action-write`. The
// phone's one write path is `mobile/src/mailbox/client.js`, and the client it
// ships has exactly one send -- `send(text)` -- which writes
// `{ id, sender, text, at, mac }`; it cannot carry a `kind` and an `action`.
// Sending a destructive command as its own text is exactly what D169 forbids, so
// this module writes nothing rather than writing the wrong thing. Carrying the
// record is P6.7's (the consumer) and P6.10's (the offline gate); until it lands,
// a confirmed action is a value the app holds. P6.9's typed path makes the same
// call for the same reason, in the same words.
//
// **A launch is not confirmed (D162) and it is not sent here either.** `propose`
// answers `pass-through` for one, and what carries it to the consumer is the same
// unwritten seam the record above waits on: the merged client writes text
// messages only, and a launch is a command rather than a sentence.
//
// **Nothing here reads a clock, a file, the network, a random source or a
// credential.** `now` (D117's server-anchored now), `newId` and the `send` port
// are injected, exactly as they are into `createMailboxClient` and
// `createTypedInput`, and the tests run with no device, no project and no
// microphone. The module's own scan (`index.test.js`) is what keeps that true.

import {
  COMMAND,
  ROUTE,
  WAKE_ALIASES,
  routeUtterance,
} from '../../../shared/command/index.js';
import {
  OUTCOME as CONFIRM_OUTCOME,
  PASS_THROUGH_VERBS,
  REFUSAL as CONFIRM_REFUSAL,
  initialState,
  isConfirmPhrase,
  pending as confirmPending,
  step,
} from '../../../shared/confirm/index.js';
import {
  INTERRUPT,
  SWITCH,
  announce,
  announcement,
  evaluateInterrupt,
} from '../../../shared/prompts/index.js';
import { isVoicePipeline } from '../audio/index.js';

/**
 * What one spoken utterance did. An outcome is not a boolean: a screen says a
 * different thing for a sentence it sent, a read-back it is waiting on and a
 * "Fugaro, ..." it did not understand, and a caller that renders an unlisted
 * string is rendering something this module never returns.
 */
export const DIALOGUE_OUTCOME = Object.freeze({
  /** The router called it agent text, and the `send` port took it. */
  SENT: 'sent',
  /** A destructive command with a resolved target: the read-back to speak. */
  READ_BACK: 'read-back',
  /** D162's launch: straight through, no read-back spoken and no record. */
  PASS_THROUGH: 'pass-through',
  /** The phrase authorised the outstanding action (see the header on delivery). */
  CONFIRMED: 'confirmed',
  /** "Fugaro, cancel that": the read-backs were dropped. */
  DISCARDED: 'discarded',
  /** The same, with nothing outstanding. A fact, not a refusal. */
  NOTHING_PENDING: 'nothing-pending',
  /** P6.1's sentence for a wake-word utterance it could not route. Not sent. */
  NOT_UNDERSTOOD: 'not-understood',
  /** "what's pending": answered by the screen, from P6.5's queue. */
  PENDING: 'pending',
  /** A command that belongs to another screen (P6.8's switcher, P3.3's list). */
  ELSEWHERE: 'elsewhere',
  /** The pause spoke: the interrupt reason's sentence, or the count. */
  ANNOUNCED: 'announced',
  /** Not a pause, or nothing to say: nothing was spoken, and nothing is lost. */
  HELD: 'held',
});

/**
 * The refusal codes this module adds to the router's, the machine's, the
 * client's and P6.5's. Everything else a refusal carries is one of theirs,
 * unchanged: one fault is one code wherever it is found.
 */
export const DIALOGUE_CODES = Object.freeze({
  /** The utterance, the options or the context is not what this module reads. */
  BAD_INPUT: 'bad-input',
  /** The utterance is not text, or is empty. */
  BAD_TEXT: 'bad-text',
  /** The clock is not a positive safe integer, so nothing can be stamped. */
  BAD_CLOCK: 'bad-clock',
  /** A field this module does not name is present -- the field set is the version. */
  UNKNOWN_FIELD: 'unknown-field',
  /** The router answered a route that is not one of its three. */
  UNKNOWN_ROUTE: 'unknown-route',
  /**
   * A confirmed action's record could not be delivered: the merged mailbox
   * client writes text messages only (see the header). Not a failure of the
   * confirmation -- the machine did its work -- but the record is not in the
   * database and the consumer has not seen it.
   */
  NO_ACTION_WRITE: 'no-action-write',
  /** The read-back did not play out, so the action it named was dropped. */
  READ_BACK_NOT_SPOKEN: 'read-back-not-spoken',
  /** Nothing was spoken because it is not a pause, or because there is nothing to say. */
  NOT_A_PAUSE: 'not-a-pause',
  /** An input made the module raise: frozen, `field: null`, and never echoed. */
  INTERNAL_ERROR: 'internal-error',
});

/** The fields `propose` reads, exactly. */
const PROPOSE_FIELDS = Object.freeze(['text', 'verb', 'target', 'actionId']);

/** The fields `confirm`'s context reads, exactly. */
const CONTEXT_FIELDS = Object.freeze(['target', 'actionId']);

/** The fields `pause` reads, exactly. */
const PAUSE_FIELDS = Object.freeze(['queues', 'active', 'digest', 'presence', 'caps', 'switch']);

/** The fields `router` may carry: P6.1's own three, and no others. */
const ROUTER_FIELDS = Object.freeze(['aliases', 'projects', 'agents']);

/** The fields `confirm` may carry: P6.2's own three, `now` excepted. */
const CONFIRM_FIELDS = Object.freeze(['timeoutMs', 'maxPending', 'maxLedger']);

/**
 * The text an `abort` carries when this module drops a read-back nobody heard.
 * It is **not** the read-back: P6.2 refuses any utterance that carries the
 * confirming phrase, and every read-back it builds ends with one ("Say
 * \"Fugaro, confirm\" to confirm."). P6.2's own words for the event are used.
 */
const DROP_TEXT = 'cancel that';

/**
 * The sentence spoken for each of P6.5's interrupt reasons. They live here and
 * not in `shared/prompts/` because P6.5 decides *what happened* -- a kill
 * switch, a cap, a consumer that went away -- and the phone decides what to say
 * about it; the prompts README is what assigns the sentences to this task.
 * `INTERRUPT.PROMPT` is deliberately absent: a due prompt is spoken as P6.5's
 * own count sentence, which is the same fact in the owner's own words.
 */
const REASON_SENTENCES = Object.freeze({
  [INTERRUPT.KILL]: 'A kill switch is set.',
  [INTERRUPT.CAP_REACHED]: 'The daily spend cap is reached.',
  [INTERRUPT.CAP_NEAR]: 'The daily spend cap is nearly reached.',
  [INTERRUPT.OFFLINE]: 'The orchestrator went offline.',
});

/** A frozen refusal, in the shape this repository's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

function isPlainObject(value) {
  if (value === null || typeof value !== 'object') return false;
  if (Array.isArray(value)) return false;
  return Object.getPrototypeOf(value) === Object.prototype;
}

/**
 * Build the spoken dialogue:
 *
 *     createVoiceDialogue({ pipeline, send, now, newId, router?, confirm? })
 *
 * `pipeline` is P3.4's (`isVoicePipeline`), and it is what speaks: the read-back,
 * the sentences, and the prompt announcement. `send` is the sink for **agent
 * text**, and the app wires it to the voice screen's own `accept` -- so the
 * screen's offline refusal (P1.4, P3.5) still stands over the voice path, and
 * the app keeps exactly one write path (P3.6). `now` is D117's server-anchored
 * now and `newId` mints the utterance, action and confirmation ids the machine
 * spends; `router` carries P6.1's configuration (`aliases`, `projects`,
 * `agents`) and `confirm` P6.2's (`timeoutMs`, `maxPending`, `maxLedger`). Both
 * are refused by the module that owns them if they are wrong.
 *
 * Three wiring failures throw rather than being discovered mid-action: a missing
 * port, a clock that is not a time (the machine's state carries a watermark, so
 * nothing could ever be judged against it), and a wake-word set none of whose
 * words P6.2 recognises beside `confirm` -- without one, every command would work
 * but the phrase that authorises an action would be refused, and the owner would
 * learn it by having a confirmation rejected.
 *
 * @param {object} options
 * @returns {object} the dialogue
 * @throws {TypeError} for a port, a value source or a configuration that cannot be used
 */
export function createVoiceDialogue(options = {}) {
  if (!isPlainObject(options)) throw new TypeError('createVoiceDialogue: an options object is required');
  const { pipeline, send, now, newId } = options;
  if (!isVoicePipeline(pipeline)) throw new TypeError('createVoiceDialogue: the pipeline (P3.4) is required');
  if (typeof send !== 'function') throw new TypeError('createVoiceDialogue: the agent-text sink is required');
  if (typeof now !== 'function') throw new TypeError('createVoiceDialogue: a clock is required');
  if (typeof newId !== 'function') throw new TypeError('createVoiceDialogue: an id source is required');

  const router = options.router === undefined ? {} : options.router;
  if (!isPlainObject(router)) throw new TypeError('createVoiceDialogue: router must be P6.1\'s options');
  for (const key of Object.keys(router)) {
    if (!ROUTER_FIELDS.includes(key)) throw new TypeError(`createVoiceDialogue: router carries no ${key}`);
  }
  const confirm = options.confirm === undefined ? {} : options.confirm;
  if (!isPlainObject(confirm)) throw new TypeError('createVoiceDialogue: confirm must be P6.2\'s options');
  for (const key of Object.keys(confirm)) {
    if (!CONFIRM_FIELDS.includes(key)) throw new TypeError(`createVoiceDialogue: confirm carries no ${key}`);
  }

  // A wake word that can never confirm is refused here. P6.2 decides whether a
  // spoken utterance IS the fixed phrase by routing it with the router's
  // **default** aliases, while this dialogue routes with the caller's -- so a
  // dialogue configured with a wake word the machine does not know would accept
  // every command and refuse the one that authorises an action. Fail-closed
  // either way; a consequence the owner would meet mid-action is what a wiring
  // failure is for. `src/text/input.js` makes the same check, in the same words.
  const aliases = router.aliases === undefined ? WAKE_ALIASES : router.aliases;
  if (!Array.isArray(aliases) || !aliases.some((one) => typeof one === 'string' && isConfirmPhrase(`${one}, confirm`))) {
    throw new TypeError('createVoiceDialogue: no configured wake word can confirm');
  }

  const start = now();
  if (!Number.isSafeInteger(start) || start <= 0) throw new TypeError('createVoiceDialogue: the clock is not a time');
  const opened = initialState({ now: start, ...confirm });
  if (!opened.ok) {
    // The machine's own refusal, carried as properties rather than through the
    // message: the message must not echo what was in the options.
    const error = new TypeError('createVoiceDialogue: the confirmation machine refused its options');
    error.code = opened.code;
    error.field = opened.field;
    throw error;
  }

  /** The machine, replaced by every accepted step and by nothing else. */
  let machine = opened.state;
  /**
   * The read-back this dialogue last **spoke**: its action id, verb, resolved
   * target (as the machine resolved it, never as the caller asserted it) and the
   * utterance as it was heard. It is what a later phrase with no context of its
   * own is about, and what a re-proposal re-runs. `null` when nothing is
   * outstanding -- never a guess, and never a value the machine did not answer.
   */
  let last = null;

  /** D117's now, or `null` if the injected clock is not a time. */
  function clock() {
    const value = now();
    return Number.isSafeInteger(value) && value > 0 ? value : null;
  }

  /** P6.1's router, over this dialogue's configuration. */
  function route(text) {
    return routeUtterance({ utterance: text, ...router });
  }

  /**
   * Speak one sentence and answer whether it played out.
   *
   * `pipeline.say` hands the text to the output port and reports its verdict, so
   * `ok: true` is "it played to the end" and everything else -- a refusal, or an
   * utterance the user's barge-in cancelled -- is "it did not". A pipeline that
   * raised is the same answer: for a sentence the speaker may have to act on,
   * "unproven" and "not heard" are the same fact.
   */
  async function speak(text) {
    try {
      const said = await pipeline.say(text);
      return said !== null && typeof said === 'object' && said.ok === true;
    } catch {
      return false;
    }
  }

  /** Whether a read-back is outstanding: the machine's own answer, at `at`. */
  function live(at) {
    const read = confirmPending(machine, at);
    return read.ok && read.actions.length > 0;
  }

  /**
   * Drop every outstanding read-back, for the one reason this module ever drops
   * one: the speaker did not hear it. A refusal from the machine (a spent
   * utterance id, a full ledger) leaves the pending where it was, and the caller
   * is told the read-back did not play, which is the fact that matters.
   */
  function dropReadBack(at) {
    const dropped = step(machine, { type: 'abort', now: at, utteranceId: newId(), text: DROP_TEXT });
    if (dropped.ok) machine = dropped.state;
    last = null;
    return dropped.ok;
  }

  /** The read-back that was just spoken, kept for the phrase that may follow it. */
  function remember(stepped, text) {
    last = Object.freeze({
      actionId: stepped.actionId,
      verb: stepped.verb,
      target: stepped.target,
      text,
    });
  }

  /**
   * Speak a read-back and wait. **The headline rule of this task is here.**
   *
   * A read-back that did not play to the end is dropped before this function
   * answers, so the action it named cannot be confirmed by a speaker who never
   * heard what it would do. See the header: the alternative -- leaving it
   * pending -- makes the phrase a confirmation of something the speaker was
   * never told, which is the failure D169's whole shape is against.
   */
  async function readBackAndWait(stepped, at, text) {
    const heard = await speak(stepped.readBack);
    if (heard === false) {
      dropReadBack(at);
      return refuse(DIALOGUE_CODES.READ_BACK_NOT_SPOKEN, 'pipeline');
    }
    remember(stepped, text);
    return Object.freeze({
      ok: true,
      outcome: DIALOGUE_OUTCOME.READ_BACK,
      verb: stepped.verb,
      actionId: stepped.actionId,
      target: stepped.target,
      readBack: stepped.readBack,
      deadline: stepped.deadline,
    });
  }

  /**
   * The fixed phrase, as its own utterance: P6.2's `confirm` event, over the
   * action the read-back is about.
   *
   * The action id is the outstanding one's (the caller's, when it named one); the
   * target is the caller's **as it is now**, or the one that was read back. A
   * merge's caller passes the head it has just re-read, and the machine compares
   * the two -- a PR that moved since the read-back is `different-target`, and
   * this module then re-proposes the command with the fresh target, so the
   * sentence spoken next names the head that is there now and the speaker
   * confirms again, having heard it. Nothing is authorised by the first phrase.
   */
  async function confirmPhrase(text, at, context) {
    const actionId = context.actionId === undefined
      ? (last === null ? undefined : last.actionId)
      : context.actionId;
    if (actionId === undefined) {
      // Nothing this dialogue read back is outstanding and the caller named no
      // action, so there is no question for the phrase to answer.
      return refuse(CONFIRM_REFUSAL.NO_PENDING_ACTION, 'actionId');
    }
    // The target the phrase is judged against: the caller's **as it is now** --
    // the head a merge's caller has just re-read (D168) -- or the one the
    // machine itself resolved into the read-back. Never one taken from the
    // speaker's words.
    const target = context.target === undefined
      ? (last === null ? undefined : last.target)
      : context.target;

    const stepped = step(machine, {
      type: 'confirm',
      now: at,
      utteranceId: newId(),
      text,
      actionId,
      confirmationId: newId(),
      ...(target === undefined ? {} : { target }),
    });
    if (!stepped.ok) {
      if (stepped.code === CONFIRM_REFUSAL.DIFFERENT_TARGET && context.target !== undefined && last !== null) {
        // The target moved between the read-back and the phrase: this is the
        // merge whose PR gained a commit while the question waited (D168).
        // Nothing is authorised; the command is proposed again and the sentence
        // spoken next names the head that is there now.
        return rePropose(at, context.target);
      }
      if (
        stepped.code === CONFIRM_REFUSAL.CONFIRMATION_EXPIRED
        || stepped.code === CONFIRM_REFUSAL.NO_PENDING_ACTION
      ) {
        // The read-back is gone -- its timeout passed, or it was already spent.
        // This dialogue stops offering it, so the phrase said later is about
        // nothing rather than about a question the speaker has forgotten.
        last = null;
      }
      return refuse(stepped.code, stepped.field);
    }
    machine = stepped.state;
    // One confirmation authorises exactly one action: the read-back is spent,
    // and the phrase said a second time authorises nothing (the machine's ledger
    // is what enforces it; forgetting it here is what keeps the offered action
    // honest).
    last = null;
    return Object.freeze({
      ok: true,
      outcome: DIALOGUE_OUTCOME.CONFIRMED,
      verb: stepped.verb,
      actionId: stepped.actionId,
      confirmationId: stepped.confirmationId,
      confirmedAt: stepped.confirmedAt,
      readBack: stepped.readBack,
      record: stepped.record,
      // The merged client has no action write, and a destructive command is
      // never sent as text. See the header; this is P6.9's own answer too.
      delivered: false,
      code: DIALOGUE_CODES.NO_ACTION_WRITE,
    });
  }

  /**
   * The target the speaker heard is not the target that exists now: drop the
   * stale read-back, run the same command again against the fresh target, and
   * speak the new read-back. It authorises nothing -- the speaker has to say the
   * phrase again, having heard the current run, repository, PR and head.
   */
  async function rePropose(at, target) {
    const stale = last;
    if (stale === null) return refuse(CONFIRM_REFUSAL.DIFFERENT_TARGET, 'target');

    if (!dropReadBack(at)) return refuse(DIALOGUE_CODES.INTERNAL_ERROR, null);
    const again = step(machine, {
      type: 'command',
      now: at,
      utteranceId: newId(),
      text: stale.text,
      verb: stale.verb,
      // The verb and the utterance are the ones that were read back; the target
      // is the caller's fresh one. The action id is new because the machine
      // remembers the first one: an action id is spent by the command it issued,
      // and two commands under one id would be an ambiguity about which action a
      // confirmation authorised.
      target,
      actionId: newId(),
    });
    if (!again.ok) return refuse(again.code, again.field);
    machine = again.state;
    const shown = await readBackAndWait(again, at, stale.text);
    if (!shown.ok) return shown;
    return Object.freeze({ ...shown, retargeted: true });
  }

  /** The routed app commands this dialogue answers itself. */
  async function appCommand(result, text, at, context) {
    if (result.command === COMMAND.CONFIRM) return confirmPhrase(text, at, context);
    if (result.command === COMMAND.CANCEL_THAT) {
      const stepped = step(machine, { type: 'abort', now: at, utteranceId: newId(), text });
      if (!stepped.ok) return refuse(stepped.code, stepped.field);
      machine = stepped.state;
      // Whatever was outstanding was just dropped: the phrase said afterwards is
      // about nothing.
      last = null;
      return Object.freeze({
        ok: true,
        outcome: stepped.outcome === CONFIRM_OUTCOME.NOTHING_PENDING
          ? DIALOGUE_OUTCOME.NOTHING_PENDING
          : DIALOGUE_OUTCOME.DISCARDED,
        actionIds: stepped.actionIds,
      });
    }
    if (result.command === COMMAND.WHATS_PENDING) {
      // The list itself is the screen's (P6.5's queue is the screen's state):
      // this dialogue says which question was asked and no more.
      return Object.freeze({ ok: true, outcome: DIALOGUE_OUTCOME.PENDING });
    }
    // switch-project, talk-to-agent, where-am-i and list are the switcher's and
    // the list screen's (P6.8, P3.3, D163's announcement). Answering them here
    // would be a second implementation of a screen that is not this one's.
    return Object.freeze({ ok: true, outcome: DIALOGUE_OUTCOME.ELSEWHERE, command: result.command });
  }

  /**
   * One final transcript: route it, then do what the route says.
   *
   * The offline refusal and the one write path stay where P3.5 and P3.6 put
   * them: agent text goes to the `send` port (the screen's `accept`), and nothing
   * else is ever sent. An app command that is not a confirmation -- "Fugaro, list
   * projects" -- is **not** agent text and is not forwarded; a wake-word
   * utterance the router could not read is answered with P6.1's sentence.
   *
   * @param {unknown} text the final transcript
   * @param {unknown} [context] what the caller knows about the target now
   * @returns {Promise<object>} the outcome, or a refusal
   */
  async function utterance(text, context = {}) {
    try {
      if (typeof text !== 'string' || text === '') return refuse(DIALOGUE_CODES.BAD_TEXT, 'text');
      if (!isPlainObject(context)) return refuse(DIALOGUE_CODES.BAD_INPUT, 'context');
      for (const key of Object.keys(context)) {
        if (!CONTEXT_FIELDS.includes(key)) return refuse(DIALOGUE_CODES.UNKNOWN_FIELD, key);
      }
      const at = clock();
      if (at === null) return refuse(DIALOGUE_CODES.BAD_CLOCK, 'now');

      const result = route(text);
      // The router's own refusal, carried unchanged: a transcript it could not
      // route is a fault with its own code, and it is not "not understood".
      if (!result.ok) return refuse(result.code, result.field);

      if (result.route === ROUTE.AGENT_TEXT) {
        const sent = await send(result.text);
        if (sent === null || typeof sent !== 'object') {
          return refuse(DIALOGUE_CODES.BAD_INPUT, 'send');
        }
        if (sent.ok !== true) {
          // The sink's own refusal, carried unchanged (P3.5's `offline` is the
          // one the owner meets): one fault is one code, and the field is the
          // sink's own.
          return refuse(sent.code ?? DIALOGUE_CODES.BAD_INPUT, sent.field === undefined ? 'send' : sent.field);
        }
        return Object.freeze({ ok: true, outcome: DIALOGUE_OUTCOME.SENT, id: sent.id, at: sent.at });
      }

      if (result.route === ROUTE.NOT_UNDERSTOOD) {
        const heard = await speak(result.reply);
        return Object.freeze({
          ok: true,
          outcome: DIALOGUE_OUTCOME.NOT_UNDERSTOOD,
          reply: result.reply,
          reason: result.reason,
          spoken: heard,
        });
      }

      if (result.route === ROUTE.APP_COMMAND) return await appCommand(result, text, at, context);
      return refuse(DIALOGUE_CODES.UNKNOWN_ROUTE, 'route');
    } catch {
      return refuse(DIALOGUE_CODES.INTERNAL_ERROR, null);
    }
  }

  /**
   * Propose a destructive command the app has already resolved, and speak its
   * read-back.
   *
   *     propose({ text, verb, target, actionId })
   *
   * `text` is the utterance as it was heard (it is the machine's
   * `command-and-confirm` check that reads it, and it is never spoken or sent),
   * `verb` is one of P6.2's closed destructive set, `target` is the resolved one
   * (a run id, or a repository, PR number and head), and `actionId` is the app's
   * own when it has one.
   *
   * A launch (D162) answers `pass-through`: no read-back, no record, and nothing
   * sent here.
   *
   * @param {unknown} input
   * @returns {Promise<object>} the read-back that was spoken, or a refusal
   */
  async function propose(input = {}) {
    try {
      if (!isPlainObject(input)) return refuse(DIALOGUE_CODES.BAD_INPUT, null);
      for (const key of Object.keys(input)) {
        if (!PROPOSE_FIELDS.includes(key)) return refuse(DIALOGUE_CODES.UNKNOWN_FIELD, key);
      }
      const { text, verb } = input;
      if (typeof text !== 'string' || text === '') return refuse(DIALOGUE_CODES.BAD_TEXT, 'text');
      const at = clock();
      if (at === null) return refuse(DIALOGUE_CODES.BAD_CLOCK, 'now');

      const stepped = step(machine, {
        type: 'command',
        now: at,
        utteranceId: newId(),
        text,
        verb,
        // D162's launch is the one verb that takes neither a target nor an action
        // id (a read-back is what names the action, and a launch has none): the
        // ids are minted for a verb that will be read back, and a caller that
        // named one keeps it -- an id on a launch is a caller that thinks it is
        // issuing something else, and the machine refuses it.
        ...(input.actionId !== undefined
          ? { actionId: input.actionId }
          : (PASS_THROUGH_VERBS.includes(verb) ? {} : { actionId: newId() })),
        ...(input.target === undefined ? {} : { target: input.target }),
      });
      if (!stepped.ok) return refuse(stepped.code, stepped.field);
      machine = stepped.state;
      if (stepped.outcome === CONFIRM_OUTCOME.PASS_THROUGH) {
        return Object.freeze({
          ok: true,
          outcome: DIALOGUE_OUTCOME.PASS_THROUGH,
          verb: stepped.verb,
          actionId: null,
        });
      }
      return await readBackAndWait(stepped, at, text);
    } catch {
      return refuse(DIALOGUE_CODES.INTERNAL_ERROR, null);
    }
  }

  /**
   * The read-backs still inside their timeout, as P6.2 recomputes them: each
   * entry's `readBack` is built from its own verb and target, so a state handed
   * back with an edited read-back cannot be spoken. What a screen renders while
   * it waits for the fixed phrase.
   */
  function pending() {
    try {
      const at = clock();
      if (at === null) return refuse(DIALOGUE_CODES.BAD_CLOCK, 'now');
      return confirmPending(machine, at);
    } catch {
      return refuse(DIALOGUE_CODES.INTERNAL_ERROR, null);
    }
  }

  /**
   * The prompt pause: what may be spoken over the user, and what is held.
   *
   *     pause({ queues, active, digest, presence, caps, switch })
   *
   * `queues` is every installation's queue (P6.5's cross-account read) and
   * `active` is the active installation's, which is the one whose due prompts are
   * marked heard and whose own interrupt policy is asked; it defaults to the
   * first of `queues`. `digest`, `presence` and `caps` are the same three signals
   * P3.5 and P3.3 supply, and `switch` is P6.5's own vocabulary (`narrow`,
   * `default`, `widen`).
   *
   * **The pause is the gate.** Nothing is spoken while the assistant is
   * speaking, while a turn is open (the user is being listened to, or the engine
   * still owes a transcript), or while a read-back is outstanding -- a queued
   * prompt is not spoken over the user, and it is not spoken over the question
   * the user still has to answer either. When it is a pause, one sentence is
   * spoken: the interrupt reason's (P6.5's policy, which is D160's wider table),
   * or the count ("Two questions waiting"). The questions stay in the queue
   * either way; what a read marks is that the active installation's due prompts
   * have been **heard**, and the updated queue is handed back for the caller to
   * keep.
   *
   * @param {unknown} input
   * @returns {Promise<object>} what was spoken, with the queue it was read from
   */
  async function pause(input = {}) {
    try {
      if (!isPlainObject(input)) return refuse(DIALOGUE_CODES.BAD_INPUT, null);
      for (const key of Object.keys(input)) {
        if (!PAUSE_FIELDS.includes(key)) return refuse(DIALOGUE_CODES.UNKNOWN_FIELD, key);
      }
      const at = clock();
      if (at === null) return refuse(DIALOGUE_CODES.BAD_CLOCK, 'now');

      const read = announcement(input.queues, { now: at });
      if (!read.ok) return refuse(read.code, read.field);

      const held = heldReason(at);
      if (held !== null) {
        return Object.freeze({
          ok: true,
          outcome: DIALOGUE_OUTCOME.HELD,
          spoken: false,
          code: DIALOGUE_CODES.NOT_A_PAUSE,
          held,
          count: read.count,
          due: read.due,
        });
      }

      const active = input.active === undefined ? input.queues[0] : input.active;
      const policy = evaluateInterrupt({
        queue: active,
        digest: input.digest === undefined ? null : input.digest,
        presence: input.presence === undefined ? null : input.presence,
        caps: input.caps === undefined ? null : input.caps,
        switch: input.switch === undefined ? SWITCH.DEFAULT : input.switch,
        now: at,
      });
      if (!policy.ok) return refuse(policy.code, policy.field);

      const marked = announce(active, { now: at });
      if (!marked.ok) return refuse(marked.code, marked.field);

      const text = policy.interrupt ? REASON_SENTENCES[policy.reason] ?? read.text : read.text;
      if (typeof text !== 'string' || text === '') {
        return Object.freeze({
          ok: true,
          outcome: DIALOGUE_OUTCOME.HELD,
          spoken: false,
          code: DIALOGUE_CODES.NOT_A_PAUSE,
          held: 'nothing-to-say',
          count: read.count,
          due: read.due,
          queue: marked.queue,
          interrupt: policy.interrupt,
          reason: policy.reason,
        });
      }

      const heard = await speak(text);
      return Object.freeze({
        ok: true,
        outcome: DIALOGUE_OUTCOME.ANNOUNCED,
        spoken: heard,
        text,
        count: read.count,
        due: read.due,
        queue: marked.queue,
        interrupt: policy.interrupt,
        reason: policy.reason,
      });
    } catch {
      return refuse(DIALOGUE_CODES.INTERNAL_ERROR, null);
    }
  }

  /**
   * Why nothing may be spoken now, or `null` when it is a pause. The three
   * reasons are the pipeline's own two flags and the machine's outstanding
   * read-backs -- each read at this moment rather than remembered.
   */
  function heldReason(at) {
    if (pipeline.speaking() === true) return 'speaking';
    if (pipeline.turn() === true) return 'user-turn';
    if (live(at)) return 'confirmation-pending';
    return null;
  }

  return Object.freeze({
    utterance,
    propose,
    pending,
    pause,
  });
}
