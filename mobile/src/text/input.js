// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The text screen's typed input (mobile plan, Task P6.9; D167, D164, D169,
// D162; functional-spec-reconciliation.md §3.5).
//
// D167's second half says what this file is: the text fallback "is the same
// pipeline with a keyboard instead of STT". The pipeline is the one the rest of
// the app already has, and this module is the keyboard's end of it and nothing
// else:
//
//   * **the router is P6.1's**, called with the same aliases, project list and
//     agent list the voice path calls it with. A typed utterance whose first
//     token is a wake-word alias is an app command, and a typed "Fugaro, …"
//     that matches no command is answered with P6.1's own sentence and **is
//     never forwarded** (D164's fail-closed rule);
//   * **the mailbox is P3.6's client**, the whole of it: a typed utterance the
//     router calls agent text is sent with `client.send`, which is the same
//     call the voice path makes -- one signed message, a fresh id, the text
//     redacted before it is signed, and the same refusal codes coming back.
//     This module composes no record and reaches no store; `write-path.test.js`
//     and `mailbox/README.md` hold the app's one write path, and a screen with
//     its own `store` would be the second one;
//   * **the confirmation machine is P6.2's**, stepped with the same events the
//     spoken path steps it with. Typing the fixed phrase is routed by P6.1 (so
//     a bare "confirm" is agent text, exactly as it is when it is spoken), and
//     the machine -- not this module -- decides whether the phrase confirms
//     anything, whether it is inside its timeout, whether the target still
//     matches and whether the action id was already spent. Every refusal below
//     is the machine's own code, carried unchanged, so a caller branches on one
//     vocabulary rather than two.
//
// ## Why the destructive target is an input and not a parse
//
// A destructive command is **resolved by the app**, not by this module, and
// that is the merged tree's own shape rather than a shortcut:
//
//   * P6.1's grammar has **no destructive rows at all**, deliberately -- its
//     README states it ("A destructive verb is not in this grammar … free text
//     cannot reach one") -- so "Fugaro, cancel run 42" is `not-understood` and
//     no typed spelling of a cancel or a merge exists to match;
//   * P6.2's machine takes the resolved target from its **caller** (`step`'s
//     command event) and builds the read-back from it and from nothing else,
//     which is what makes D47's Rule hold: the read-back names the run id that
//     was resolved and never the transcript;
//   * nothing in the merged tree resolves a verb and a target from an
//     utterance, and nothing may invent one here: a target guessed from the
//     user's own words is exactly the "paraphrase" the read-back is forbidden
//     to be.
//
// So `propose` takes a resolved action from the app (the run id the app looked
// up, the repository, PR number and head it read) and this module does the half
// that is the phone's: it runs that command through the machine, and the human
// types the fixed phrase. The typed half of D169's rule is the phrase, and the
// phrase is typed here.
//
// ## The one thing a confirmed action does NOT do yet, and who now owns it
//
// `propose` → typed "Fugaro, confirm" produces P6.3's **action record** (the
// machine builds it), and the record is **returned by this module, not
// written**: every `confirmed` answer carries `delivered: false` and `code:
// 'no-action-write'`. That is still true, and the code keeps its meaning -- the
// record is not in the database and the consumer has not seen it.
//
// **What has changed is the reason, and this header used to state the old one.**
// It said the client "has exactly one send -- `send(text)`" and "has no way to
// carry a `kind` and an `action`". That was true of P3.6's client and is **not
// true of the merged tree any more**: P6.10 added exactly that write,
// `client.send(text, { action })`, which writes P6.3's action record (`kind:
// 'action'`, the resolved target, the confirmation record) and keeps it out of
// the retry queue on purpose -- D167's "a destructive action record is never
// queued at all", which is the clause of P6.10's verification this send exists
// for. So the capability a confirmed action needs is in the tree; what is
// missing is the **wiring**.
//
// **Who owns the wiring.** Nothing in this module calls that send, and adding
// the call here would be a decision about the screens, not about this file:
// what the owner is shown when the gate refuses the record (it is then DROPPED
// -- an action is never queued, so there is no retry to offer) and that the
// confirmation is spent either way. Those belong to the screens that own a
// confirm path: **P6.9's own remaining half** for this text path, and **P6.12**
// (the spoken confirmation in the voice screen, approved 2026-10-18 and not yet
// built) for the other. Neither plan line names the delivery explicitly -- both
// are written about the confirmation machine rather than about the write that
// follows it -- so this is a gap the plan has and not a task the plan assigns;
// the pull request that added `send(text, { action })` says so in its body
// rather than quietly leaving this file claiming the capability is absent.
//
// What does NOT change: sending a destructive command as its own text would be
// precisely what D169 forbids ("typed action records … not text to the agent",
// and the consumer refuses destructive free text it recognises), so this module
// writes nothing rather than writing the wrong thing. Until the wiring lands, a
// confirmed action is a value the app holds and can act on, and the text screen
// shows it as confirmed with the reason it was not sent.
//
// Nothing here reads a clock, a file, the network, a random source or a
// credential: `now` and `newId` are injected, exactly as they are into
// `createMailboxClient`, and the tests run with no device and no project.

import { COMMAND, ROUTE, WAKE_ALIASES, routeUtterance } from '../../../shared/command/index.js';
import {
  CONFIRM_CUE,
  DESTRUCTIVE_VERBS,
  OUTCOME as CONFIRM_OUTCOME,
  REFUSAL as CONFIRM_REFUSAL,
  initialState,
  isConfirmPhrase,
  pending as confirmPending,
  step,
} from '../../../shared/confirm/index.js';

/**
 * What one typed utterance did. An outcome is not a boolean: the screen says a
 * different thing for a sentence it sent and for a "Fugaro, …" it did not
 * understand, and a caller that renders an unlisted string is rendering
 * something this module never returns.
 */
export const INPUT_OUTCOME = Object.freeze({
  /** The router called it agent text, and the mailbox client took it. */
  SENT: 'sent',
  /** A destructive command with a resolved target: the read-back to show. */
  READ_BACK: 'read-back',
  /** The typed phrase authorised the outstanding action (see the header). */
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
});

/**
 * The refusal codes this module adds to the router's, the machine's and the
 * client's. Everything else a refusal carries is one of theirs, unchanged: one
 * fault is one code wherever it is found.
 */
export const INPUT_CODES = Object.freeze({
  /** The utterance is not text, or is empty. */
  BAD_TEXT: 'bad-text',
  /** The clock is not a positive safe integer, so nothing can be stamped. */
  BAD_CLOCK: 'bad-clock',
  /** The router answered a route that is not one of its three. */
  UNKNOWN_ROUTE: 'unknown-route',
  /**
   * A confirmed action's record was not delivered: **nothing here sends it**
   * (see the header -- the client has carried an action write since P6.10, and
   * the call belongs to the screens, P6.9's remaining half and P6.12). Not a
   * failure of the confirmation -- the machine did its work -- but the record is
   * not in the database and the consumer has not seen it.
   */
  NO_ACTION_WRITE: 'no-action-write',
});

/** The words the text screen puts under a read-back. P6.2's cue says "Say";
 * a keyboard does not, and a cue that asked the user to speak on a text screen
 * would be the one instruction this screen cannot follow. */
export const TYPE_CUE = 'Type "Fugaro, confirm" to confirm.';

/**
 * The read-back, retargeted at a keyboard.
 *
 * P6.2's `readBack` is one sentence **and** one spoken cue: every read-back it
 * builds ends `"… Say \"Fugaro, confirm\" to confirm."` (its `CONFIRM_CUE`),
 * which is right for the spoken path and wrong here. Drawing that sentence and
 * `TYPE_CUE` under it -- which is what this module did until this round -- puts
 * a "Say" instruction and a "Type" instruction on the same screen, one above
 * the other, on the screen that exists because the user cannot speak to it.
 *
 * So the cue is **swapped where the read-back is produced rather than where it
 * is drawn**: the sentence that names the resolved target (D47's Rule) is
 * P6.2's and is carried through character for character, and only the
 * instruction at the end changes. The cue is P6.2's own exported constant,
 * imported rather than respelled, and a read-back that does not end in it is
 * returned **unmodified** -- a sentence this module does not recognise is not
 * one it will edit, because the only safe edit is one that provably touches
 * nothing but the instruction.
 */
export function typedReadBack(readBack) {
  if (typeof readBack !== 'string') return readBack;
  if (!readBack.endsWith(CONFIRM_CUE)) return readBack;
  const spoken = readBack.slice(0, -CONFIRM_CUE.length).trimEnd();
  // A read-back that is nothing but the cue would otherwise be drawn with a
  // leading space. P6.2 never builds one (its sentence always names the
  // target), but the space would be this function's own doing.
  return spoken === '' ? TYPE_CUE : `${spoken} ${TYPE_CUE}`;
}

function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

function isPlainObject(value) {
  if (value === null || typeof value !== 'object') return false;
  if (Array.isArray(value)) return false;
  return Object.getPrototypeOf(value) === Object.prototype;
}

/** The fields `router` may carry: P6.1's own three, and no others. */
const ROUTER_FIELDS = Object.freeze(['aliases', 'projects', 'agents']);

/** The fields `confirm` may carry: P6.2's own four, `now` excepted. */
const CONFIRM_FIELDS = Object.freeze(['timeoutMs', 'maxPending', 'maxLedger']);

/** The fields `propose` reads, exactly. A field this module does not name is
 * refused rather than ignored, so a caller that spelled `target` wrongly hears
 * about it instead of reading back the wrong thing. */
const PROPOSE_FIELDS = Object.freeze(['text', 'verb', 'target', 'actionId']);

/** The fields `submit`'s context reads, exactly. */
const CONTEXT_FIELDS = Object.freeze(['target', 'actionId']);

/**
 * Build the typed pipeline:
 *
 *     createTypedInput({ mailbox, now, newId, router?, confirm? })
 *
 * `mailbox` is P3.6's client -- the SAME client the voice path is built on --
 * `now` is D117's server-anchored now, and `newId` mints the ids the
 * confirmation machine spends (an utterance id per call, an action id per
 * proposed command, a confirmation id per phrase). `router` carries P6.1's
 * configuration (`aliases`, `projects`, `agents`) and `confirm` P6.2's
 * (`timeoutMs`, `maxPending`, `maxLedger`); both are refused by the module that
 * owns them if they are wrong.
 *
 * The one thing checked here rather than passed on is the clock: the machine's
 * state carries a watermark, so a screen built without a working clock would be
 * one no event could ever be judged against. A bad clock is a wiring failure
 * and throws, exactly as `createMailboxClient` throws for a bad interval.
 */
export function createTypedInput(options = {}) {
  if (!isPlainObject(options)) {
    throw new TypeError('createTypedInput: an options object is required');
  }
  const { mailbox, now, newId } = options;
  if (mailbox === null || typeof mailbox !== 'object' || typeof mailbox.send !== 'function') {
    throw new TypeError('createTypedInput: the mailbox client (with send()) is required');
  }
  if (typeof now !== 'function') throw new TypeError('createTypedInput: a clock is required');
  if (typeof newId !== 'function') throw new TypeError('createTypedInput: an id source is required');

  const router = options.router === undefined ? {} : options.router;
  if (!isPlainObject(router)) throw new TypeError('createTypedInput: router must be P6.1\'s options');
  for (const key of Object.keys(router)) {
    if (!ROUTER_FIELDS.includes(key)) {
      throw new TypeError(`createTypedInput: router carries no ${key}`);
    }
  }
  const confirm = options.confirm === undefined ? {} : options.confirm;
  if (!isPlainObject(confirm)) throw new TypeError('createTypedInput: confirm must be P6.2\'s options');
  for (const key of Object.keys(confirm)) {
    if (!CONFIRM_FIELDS.includes(key)) {
      throw new TypeError(`createTypedInput: confirm carries no ${key}`);
    }
  }

  // **A wake word that can never confirm is refused here.** P6.2's machine
  // decides whether a typed utterance IS the fixed phrase by routing it with
  // the router's **default** aliases (`isConfirmPhrase`: "It is P6.1's answer,
  // not a second grammar"), while this screen routes with the caller's. So a
  // screen configured with a wake word the machine does not know -- say
  // `aliases: ['Hey']` -- routes "Hey, confirm" as P6.1's `confirm` command and
  // then has the machine refuse it `not-a-confirmation`: the owner's own wake
  // word could be used for every command except the one that authorises an
  // action, and they would learn it only by having a confirmation rejected.
  //
  // The behaviour is fail-closed either way -- nothing is authorised, and the
  // refusal is the machine's own -- so this is not a hole. It is a
  // configuration whose consequence the caller cannot see, which is exactly
  // what a wiring failure is for: at least one configured wake word must be one
  // the machine recognises beside `confirm`, or this is refused where the app
  // is being wired rather than discovered by a person mid-action.
  const aliases = router.aliases === undefined ? WAKE_ALIASES : router.aliases;
  if (
    !Array.isArray(aliases) ||
    !aliases.some((one) => typeof one === 'string' && isConfirmPhrase(`${one}, confirm`))
  ) {
    throw new TypeError('createTypedInput: no configured wake word can confirm');
  }

  const start = now();
  if (!Number.isSafeInteger(start) || start <= 0) {
    throw new TypeError('createTypedInput: the clock is not a time');
  }
  const opened = initialState({ now: start, ...confirm });
  if (!opened.ok) {
    // The machine's own refusal, carried as properties rather than through the
    // message: the message must not echo what was in the options.
    const error = new TypeError('createTypedInput: the confirmation machine refused its options');
    error.code = opened.code;
    error.field = opened.field;
    throw error;
  }

  /** The machine, replaced by every accepted step and by nothing else. */
  let machine = opened.state;

  /** D117's now, or `null` if the injected clock is not a time. */
  function clock() {
    const value = now();
    return Number.isSafeInteger(value) && value > 0 ? value : null;
  }

  /** P6.1's router, over this screen's configuration. */
  function route(text) {
    return routeUtterance({ utterance: text, ...router });
  }

  /** One utterance the router called agent text: the mailbox client's `send`,
   * which is the same call the voice path makes and the app's one write. */
  async function send(text) {
    const sent = await mailbox.send(text);
    if (!sent.ok) return refuse(sent.code, sent.field);
    return Object.freeze({ ok: true, outcome: INPUT_OUTCOME.SENT, id: sent.id, at: sent.at });
  }

  /**
   * The fixed phrase, as its own utterance: P6.2's `confirm` event, over the
   * action the read-back is about.
   *
   * The `actionId` is the outstanding action's own. The screen holds it -- it
   * is the read-back the screen is drawing, and `propose` returned it -- and
   * passes it as `context.actionId`. When the caller has none, the single live
   * read-back's id is used, because a screen with exactly one thing waiting has
   * exactly one thing the phrase could mean; with two outstanding the machine
   * refuses (`ambiguous-pending`), and this module does not pick between them,
   * which is what keeps the choice the machine's.
   *
   * The deadline is the machine's to judge, and that is why a caller holding a
   * read-back past its deadline passes the id: `pending` sweeps a timed-out
   * read-back out, and the machine -- asked for the id it no longer holds --
   * then names the timeout (`confirmation-expired`) where a swept list could
   * only say "nothing is pending". The difference is the whole reason the
   * machine refuses to sweep on a confirm. Such a caller passes the target too
   * (it is the read-back it drew: the run id, or the head it re-read), because
   * a read-back that is no longer live is no longer a source to re-state it
   * from -- and a confirmation that re-states nothing is `unresolved-target`,
   * which is the machine saying it cannot read back what was not said.
   *
   * `context.target` is the target **as it is now**, and it defaults to the one
   * that was read back. A merge's caller passes the head it has just re-read,
   * and the machine compares the two: a PR that moved since the read-back is
   * refused (`different-target`) rather than merged against a head the speaker
   * never heard.
   */
  function confirmPhrase(text, at, context) {
    let { actionId } = context;
    let readBackTarget;
    // The read-back the phrase is about is looked up only to fill in what the
    // caller did not say -- which id, and (when re-stating is not the caller's
    // business) which target. A caller that names an action id this machine no
    // longer holds live is passed straight through, so the machine answers with
    // why rather than this module guessing.
    if (actionId === undefined || context.target === undefined) {
      const live = confirmPending(machine, at);
      if (!live.ok) return refuse(live.code, live.field);
      const outstanding = actionId === undefined
        ? live.actions[0]
        : live.actions.find((one) => one.actionId === actionId);
      if (actionId === undefined) {
        if (outstanding === undefined) {
          // The machine's own code for this (it answers it when the action id
          // names nothing outstanding); this module never mints an id to ask
          // with, so it answers the same code directly rather than inventing
          // one.
          return refuse(CONFIRM_REFUSAL.NO_PENDING_ACTION, 'actionId');
        }
        actionId = outstanding.actionId;
      }
      readBackTarget = outstanding === undefined ? undefined : outstanding.target;
    }
    const stepped = step(machine, {
      type: 'confirm',
      now: at,
      utteranceId: newId(),
      text,
      actionId,
      confirmationId: newId(),
      target: context.target === undefined ? readBackTarget : context.target,
    });
    if (!stepped.ok) return refuse(stepped.code, stepped.field);
    machine = stepped.state;
    return Object.freeze({
      ok: true,
      outcome: INPUT_OUTCOME.CONFIRMED,
      verb: stepped.verb,
      actionId: stepped.actionId,
      confirmationId: stepped.confirmationId,
      confirmedAt: stepped.confirmedAt,
      record: stepped.record,
      // See the header: nothing in this module sends the record (the client's
      // action write landed with P6.10 and the call is the screens'), and a
      // destructive command is never sent as its own text either.
      delivered: false,
      code: INPUT_CODES.NO_ACTION_WRITE,
    });
  }

  /** The routed app commands this screen answers itself. */
  function appCommand(result, text, at, context) {
    if (result.command === COMMAND.CONFIRM) return confirmPhrase(text, at, context);
    if (result.command === COMMAND.CANCEL_THAT) {
      const stepped = step(machine, { type: 'abort', now: at, utteranceId: newId(), text });
      if (!stepped.ok) return refuse(stepped.code, stepped.field);
      machine = stepped.state;
      return Object.freeze({
        ok: true,
        outcome: stepped.outcome === CONFIRM_OUTCOME.NOTHING_PENDING
          ? INPUT_OUTCOME.NOTHING_PENDING
          : INPUT_OUTCOME.DISCARDED,
        actionIds: stepped.actionIds,
      });
    }
    if (result.command === COMMAND.WHATS_PENDING) {
      // The list itself is the screen's (P6.5's queue is the screen's state):
      // this pipeline says which question was asked and no more.
      return Object.freeze({ ok: true, outcome: INPUT_OUTCOME.PENDING });
    }
    // switch-project, talk-to-agent, where-am-i and list are the switcher's and
    // the list screen's (P6.8, P3.3). Answering them here would be a second
    // implementation of a screen that is not this one's.
    return Object.freeze({ ok: true, outcome: INPUT_OUTCOME.ELSEWHERE, command: result.command });
  }

  /** One typed utterance: route it, then do what the route says. */
  async function submit(text, context = {}) {
    if (typeof text !== 'string' || text === '') return refuse(INPUT_CODES.BAD_TEXT, 'text');
    if (!isPlainObject(context)) return refuse(INPUT_CODES.BAD_TEXT, 'context');
    for (const key of Object.keys(context)) {
      if (!CONTEXT_FIELDS.includes(key)) return refuse(INPUT_CODES.BAD_TEXT, key);
    }
    const at = clock();
    if (at === null) return refuse(INPUT_CODES.BAD_CLOCK, 'now');

    const result = route(text);
    // The router's own refusal, carried unchanged: a transcript it could not
    // route is a fault with its own code, and it is not "not understood".
    if (!result.ok) return refuse(result.code, result.field);

    if (result.route === ROUTE.AGENT_TEXT) return send(result.text);
    if (result.route === ROUTE.NOT_UNDERSTOOD) {
      return Object.freeze({
        ok: true,
        outcome: INPUT_OUTCOME.NOT_UNDERSTOOD,
        reply: result.reply,
        reason: result.reason,
      });
    }
    if (result.route === ROUTE.APP_COMMAND) return appCommand(result, text, at, context);
    return refuse(INPUT_CODES.UNKNOWN_ROUTE, 'route');
  }

  /**
   * A destructive command the app has resolved, handed to the same machine the
   * spoken path uses:
   *
   *     propose({ text, verb, target?, actionId? })
   *
   * `text` is what the user typed or the app's own words for the action; it is
   * carried into the event because P6.2 spends an utterance id and refuses a
   * text that carries the confirming phrase beside a command. `verb` and
   * `target` are the app's resolution (see the header).
   *
   * `actionId` is the id the confirmation will name, and it is minted here from
   * `newId` when the caller has none. It is minted **only for P6.2's own
   * destructive verbs** (its `DESTRUCTIVE_VERBS`, imported rather than
   * respelled -- a local copy of that table is exactly the fork this module is
   * forbidden to make): a destructive command without an action id is
   * `missing-field` to the machine, because there would be no id for "one
   * confirmation authorises exactly one action" to be about, while a launch
   * carrying one is `verb-target-mismatch`, because a launch is not read back
   * at all. Which verbs those are is P6.2's answer, not this module's.
   *
   * A **launch** (D162) has no read-back and no record: it passes through the
   * machine and goes to the agent as text, through the same mailbox client --
   * which is what "no confirmation" means on the wire as well as on the screen.
   */
  async function propose(input = {}) {
    if (!isPlainObject(input)) return refuse(INPUT_CODES.BAD_TEXT, 'propose');
    for (const key of Object.keys(input)) {
      if (!PROPOSE_FIELDS.includes(key)) return refuse(INPUT_CODES.BAD_TEXT, key);
    }
    const text = input.text;
    if (typeof text !== 'string' || text === '') return refuse(INPUT_CODES.BAD_TEXT, 'text');
    const at = clock();
    if (at === null) return refuse(INPUT_CODES.BAD_CLOCK, 'now');

    const destructive = DESTRUCTIVE_VERBS.includes(input.verb);
    // Minted before the utterance id is, so the id the confirmation will name
    // is the first one the caller's source hands out -- and only ever for a
    // verb the machine reads back, because a launch carrying one is refused.
    const actionId = input.actionId === undefined && destructive ? newId() : input.actionId;
    const event = {
      type: 'command',
      now: at,
      utteranceId: newId(),
      text,
      verb: input.verb,
    };
    if (input.target !== undefined) event.target = input.target;
    if (actionId !== undefined) event.actionId = actionId;

    const stepped = step(machine, event);
    if (!stepped.ok) return refuse(stepped.code, stepped.field);
    machine = stepped.state;

    if (stepped.outcome === CONFIRM_OUTCOME.PASS_THROUGH) {
      // D162: a launch goes straight through, and the caps are what bound it.
      return send(text);
    }
    return Object.freeze({
      ok: true,
      outcome: INPUT_OUTCOME.READ_BACK,
      actionId: stepped.actionId,
      verb: stepped.verb,
      target: stepped.target,
      deadline: stepped.deadline,
      // The machine's own sentence, with the typed cue (see `typedReadBack`).
      readBack: typedReadBack(stepped.readBack),
    });
  }

  /**
   * What is waiting for the typed phrase: P6.2's `pending`, which recomputes
   * every read-back from its own verb and target rather than reading one out of
   * the state. The screen draws these -- each with the typed cue, exactly as
   * `propose` hands one back -- and a caller that has a `target` to re-state
   * passes it to `submit`.
   */
  function awaiting() {
    const at = clock();
    if (at === null) return refuse(INPUT_CODES.BAD_CLOCK, 'now');
    const live = confirmPending(machine, at);
    if (!live.ok) return refuse(live.code, live.field);
    return Object.freeze({
      ok: true,
      actions: Object.freeze(live.actions.map((one) => Object.freeze({
        ...one,
        readBack: typedReadBack(one.readBack),
      }))),
    });
  }

  return Object.freeze({ ok: true, submit, propose, awaiting });
}
