// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The ten never-send rules, and the only door a turn leaves by (mobile plan,
// Task P5.3.7 -- SECURITY-SENSITIVE; D118, D111, D123, D124; the design is
// docs/mobile/design/voice-evaluation-switch.md §5, §5 rule 4's residual, §4.5
// and §6).
//
// **This file is the list, as code.** §5's ten rules are the limits *inside*
// §1a's amendment: the amendment is what permits an upload at all, and these
// are what it permits **of**. Each one is a branch below and each one has a test
// in `egress.test.js`, named after the rule it holds:
//
//   1. a spoken confirmation's audio, and anything while a confirmation is
//      pending -- rules 2 and 3 make it structural rather than a filter, so
//      there is deliberately no branch here that recognises "a confirmation":
//      the app cannot know which utterance will turn out to be one until it has
//      been transcribed, and by then the audio is gone;
//   2. nothing at all while a confirmation is pending -- `pending(state, now)`
//      is asked about the **state**, never about a phrase;
//   3. nothing on a destructive-command path, and the read-back and the
//      confirmation are always local -- a route that acts on a pending action
//      (`shared/confirm`'s two commands) is served entirely locally, and while a
//      destructive action is pending rule 2 freezes everything anyway, which is
//      what makes the read-back (`readBack(verb, target)`, rebuilt from the
//      resolved target and never from a transcript) and the confirming
//      utterance local;
//   4. a secret a redaction rule matches -- **substituted, never dropped**, and
//      reachable **only on the text path**: `redact` runs on what this app sends
//      as text, and the audio path is not gated by it and is not claimed to be;
//   5. nothing while no command is intended -- the **route** decides: an
//      utterance the router did not understand, and one it understood as one of
//      the app's own commands, are both served locally;
//   6. speech about a project that is not on the list;
//   7. another installation's speech -- `belong` is never a target;
//   8. the console's traffic -- this app's voice egress is the hop and nothing
//      else, and `hop.js` is the only file in the app that opens a request;
//   9. any credential -- the request carries the Firebase ID token and no other
//      credential, and there is no provider key on the device to carry;
//  10. any audio the user has not just spoken as a command -- one turn is one
//      utterance, a failure is terminal, and nothing is queued, spooled, filed
//      or buffered (which is also the scan `boundary.test.js` holds over this
//      directory).
//
// **What replaces a filter.** Three of the ten are properties of *this module's
// shape* rather than of a branch in it, and they are stated where they can be
// checked rather than promised in prose: rule 8 is the scanner rule that no file
// but `hop.js` calls `fetch`, rule 9 is the `Authorization` header `hop.js`
// builds and nothing else, and rule 10 is the absence of a timer, a file, a
// store or an array in this module that could hold a turn past the one it
// belongs to. A design that could only express these as promises would be a
// design that had not understood them.
//
// **The residual, stated honestly, because the app cannot do better.** Nothing
// here can classify an utterance before it has been transcribed, and on a device
// whose local engine cannot hear the user the only transcript is the remote one.
// So the *first* utterance of a destructive command ("Fugaro, cancel the docs
// refactor") may have been transcribed remotely before anything knew it was
// destructive. What rule 2 still guarantees is the thing that matters: from the
// moment a read-back exists a confirmation is pending, nothing goes at all, and
// **a remote engine can never be the source of the utterance that confirms**.
// The design says this in §5 rule 3 and this file does not soften it: `permit`
// takes the route it is given and answers about *that* turn.
//
// **Where the device's half and the hop's half come from.** The switch, the
// recorded consent, the hop's last project-status answer and the last call are
// not read here: they are `shared/voice-switch`'s badge, and `voiceSwitch`'s
// `indicator` is what reads them against the record the device actually holds
// (P5.3.6's rule -- "the consent it judges is the one this app recorded"). This
// file takes the badge and adds only what the badge cannot know: the state, the
// route and the project. Two halves, one answer, and neither can widen the
// other.

import { ROUTE } from '../../../shared/command/index.js';
import { DESTRUCTIVE_VERBS, pending, readBack } from '../../../shared/confirm/index.js';
import { REDACTED, redact } from '../../../shared/digest/index.js';
import { CALL_FAILURE, CALL_STATE, ENGINE, REFUSAL, REASON } from '../../../shared/voice-switch/index.js';

/**
 * The ten rules of §5, as data.
 *
 * They are exported as a list because the plan asks for ten and a reader should
 * not have to count branches: `egress.test.js` pins this array, and each name in
 * it is a test's name. The order is the design's, and it is the order the
 * branches below take as far as the rules' subjects allow.
 */
export const EGRESS_RULES = Object.freeze([
  'confirmation-audio',
  'confirmation-pending',
  'destructive-route',
  'redaction-substitutes',
  'no-command',
  'foreign-project',
  'other-installation',
  'console-traffic',
  'no-credential',
  'no-spool',
]);

/**
 * Why a turn this app holds back is served locally instead.
 *
 * The four here are the ones the badge cannot answer -- they are about *this
 * turn*, not about the device or the project: a confirmation is pending, the
 * route is destructive, the route is not one that goes anywhere, or the project
 * is not this account's to speak of. Every other reason a turn stays local is
 * the badge's own (`switch-local`, `no-consent`, `no-answer`, `force-local`,
 * `expired-token`, ...) and is carried through **by name** rather than
 * re-spelled, so a caller that shows the reason shows the module's own word for
 * it and the two vocabularies cannot drift apart.
 */
export const EGRESS_CODE = Object.freeze({
  /** A confirmation is pending: **nothing** goes (§5 rules 1 and 2). */
  PENDING: 'confirmation-pending',
  /** The route acts on a pending action; the whole path is local (§5 rule 3). */
  DESTRUCTIVE: 'destructive-route',
  /** The router did not understand it, or understood it as the app's own command (§5 rule 5). */
  NO_COMMAND: 'no-command',
  /** The project is not one this account may speak of (§5 rule 6). */
  FOREIGN_PROJECT: 'foreign-project',
  /**
   * The project is this app's own other installation, which no agent touches
   * (Global Constraint 11, §5 rule 7). It is refused by name and not by the
   * list, because a list is editable and this must not be.
   */
  OTHER_INSTALLATION: 'other-installation',
  /** The router could not read the utterance, so no route may be acted on. */
  ROUTE_UNREADABLE: 'route-unreadable',
  /**
   * The confirmation machine's state could not be read, so whether a
   * confirmation is pending is unknown -- and unknown is **not** "nothing is
   * pending" (`shared/confirm`'s own fail-closed rule, and §4.5's).
   */
  STATE_UNREADABLE: 'state-unreadable',
  /**
   * The redactor refused the text it was handed. The turn is served locally
   * rather than sent: §5 rule 4 makes the redactor's output the thing that goes,
   * and text whose redaction failed is text whose redaction did not happen.
   */
  REDACTOR_FAILED: 'redactor-failed',
  /** No recorded consent for any provider the project admits (the badge's own reason). */
  NO_PROVIDER: 'no-provider',
});

/** What the app owes the user when the local engine is the one that failed. */
export const EGRESS_OFFER = Object.freeze({
  /** §4.5's last row: "the local engine also fails -- the app says so and offers text entry". */
  TEXT_ENTRY: 'text-entry',
});

/**
 * The project this app never speaks of (Global Constraint 11, §5 rule 7).
 *
 * `belong` is the owner's own second installation, added by hand and never
 * touched by an agent (F31). It is refused **by name** rather than left to the
 * configured list, for the reason the constraint exists: a list is configuration
 * and configuration changes, and a name that must never be a target should not
 * be one because an edit happened to leave it out.
 */
export const RESERVED_PROJECTS = Object.freeze(['belong']);

/**
 * The two of `shared/command`'s seven commands that act on a pending action.
 *
 * They are the destructive path in rule 3's own mechanism: the router resolves
 * the utterance, and a route that confirms or abandons an action is served
 * entirely locally, because the action it acts on was read back by the app and
 * its authority is local (§6 points 1 to 4). The verbs themselves
 * (`shared/confirm`'s `DESTRUCTIVE_VERBS`) are what name an action destructive,
 * and they are re-exported below so that a reader of this file can see the two
 * ends of the chain in one place.
 */
export const ACTION_COMMANDS = Object.freeze(['confirm', 'cancel-that']);

/** The two directions, as `shared/voice-switch` names them. */
const DIRECTIONS = Object.freeze(['stt', 'tts']);

/** A frozen refusal, in the shape this repository's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/** Whether a value is a plain object this module will read. */
function isPlainObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

/** A local outcome: the turn did not go, and here is why. */
function local(direction, reason, offer = null) {
  return Object.freeze({
    ok: true,
    send: false,
    engine: ENGINE.LOCAL,
    direction,
    reason,
    provider: null,
    offer,
  });
}

/**
 * Whether this turn may leave the device, and if not, why it is served locally.
 *
 * **Pure**, and the whole of the ten rules that a decision can be: it reads no
 * store, opens no socket and holds no state, so a test can put a turn in front
 * of it and read the answer. What it does not answer -- the device's setting,
 * the recorded consent, the project's policy, the last call -- arrives as the
 * badge, read from the record by `voiceSwitch.indicator` before this is called.
 *
 * The rules are taken in the order that makes each one's answer the one a caller
 * should hear first: a confirmation pending is a fact about the world that no
 * route, project or badge can outrank; a destructive route is next, because it
 * is the one the design says must be decided by the router; then the route, then
 * the project, then the badge.
 *
 * @param {object} input
 * @param {string} input.direction `stt` or `tts`
 * @param {object|null} input.route the result of `routeUtterance` for this
 *   utterance, or a refusal from it, or `null` when this turn has no transcript
 *   of its own yet (a remote-STT turn that has not answered)
 * @param {object|null} input.state the confirmation machine (`shared/confirm`),
 *   or `null` when the app has no action history at all
 * @param {number} input.now the caller's clock, for `pending(state, now)`
 * @param {string|null} input.project the slug this turn would be sent for
 * @param {readonly string[]} input.projects the slugs this account may speak of
 * @param {object} input.badge the answer `voiceSwitch.indicator` gave
 * @returns {{ok: true, send: true, engine: string, direction: string,
 *            provider: string|null, reason: null, offer: null}
 *           | {ok: true, send: false, engine: 'local', direction: string,
 *              provider: null, reason: string, offer: null}
 *           | {ok: false, code: string, field: string|null}}
 */
export function permit({ direction, route, state, now, project, projects, badge } = {}) {
  if (!DIRECTIONS.includes(direction)) return refuse(REFUSAL.UNKNOWN_DIRECTION, 'direction');
  if (!Array.isArray(projects)) return refuse(REFUSAL.INTERNAL_ERROR, 'projects');
  if (!isPlainObject(badge) || badge.ok !== true) return refuse(REFUSAL.INTERNAL_ERROR, 'badge');

  // Rules 1 and 2, and they come first. `pending` is asked about the **state**:
  // it sweeps the deadline against `now` and answers the actions that are still
  // live, so a phrase is never what decides, and an action past its deadline is
  // not a confirmation any more (which is exactly the 60 s bound of §6 point 3).
  // A state this app cannot read is not a state that says "nothing is pending":
  // the whole turn is local, and so is every turn after it until the state reads.
  if (state !== null && state !== undefined) {
    const live = pending(state, now);
    if (!live.ok) return local(direction, EGRESS_CODE.STATE_UNREADABLE);
    if (live.actions.length > 0) return local(direction, EGRESS_CODE.PENDING);
  }

  // Rule 3. A route this module cannot read is a route it will not act on, and
  // the two commands that act on a pending action are the destructive path: the
  // read-back is rebuilt by the app (`readBack`, which is imported above so that
  // this file cannot drift from what the app speaks) and the confirming utterance
  // is transcribed locally. Nothing here recognises a destructive *phrase* --
  // §5 rule 3's mechanism is the router, and the verbs name the action, not the
  // words.
  if (!isPlainObject(route)) return local(direction, EGRESS_CODE.ROUTE_UNREADABLE);
  if (route.ok !== true) return local(direction, EGRESS_CODE.ROUTE_UNREADABLE);
  if (route.route === ROUTE.NOT_UNDERSTOOD) return local(direction, EGRESS_CODE.NO_COMMAND);
  if (route.route === ROUTE.APP_COMMAND) {
    return local(
      direction,
      ACTION_COMMANDS.includes(route.command) ? EGRESS_CODE.DESTRUCTIVE : EGRESS_CODE.NO_COMMAND,
    );
  }
  if (route.route !== ROUTE.AGENT_TEXT) return local(direction, EGRESS_CODE.ROUTE_UNREADABLE);

  // Rules 6 and 7. A turn goes for one project and only that project: the slug
  // is what the hop reads the policy node by (the hop takes the project from the
  // request, §4.4, so this is a guard against accident and stale state and not a
  // boundary against the device's owner), and it must be a slug this account
  // holds. `belong` is refused by name, before the list is consulted.
  if (typeof project !== 'string' || project.length === 0) return local(direction, EGRESS_CODE.FOREIGN_PROJECT);
  if (RESERVED_PROJECTS.includes(project)) return local(direction, EGRESS_CODE.OTHER_INSTALLATION);
  if (!projects.includes(project)) return local(direction, EGRESS_CODE.FOREIGN_PROJECT);

  // Rules 4, 8 and 9 are properties of what is *sent* rather than of whether it
  // goes, and they are held at the call below: the payload is the redactor's
  // output on the text path, the request is the hop's and no other, and its only
  // credential is the ID token. What is left to the badge is the switch, the
  // consent and the project's own answer.
  if (badge.send !== true) return local(direction, badge.reason ?? REASON.NO_ANSWER);
  if (typeof badge.provider !== 'string' || badge.provider.length === 0) {
    // A remote route with no provider name is not a route: the hop's forwarding
    // path is `/stt/<engine>` and `<engine>` is the provider's register name, so
    // there is no call to make and the device's own answer ("no consent") is the
    // honest one.
    return local(direction, EGRESS_CODE.NO_PROVIDER);
  }

  return Object.freeze({
    ok: true,
    send: true,
    engine: ENGINE.REMOTE,
    direction,
    provider: badge.provider,
    reason: null,
    offer: null,
  });
}

/**
 * The hop's refusal, in the badge's vocabulary.
 *
 * `shared/voice-switch`'s `lastCallInDirection.reason` is a closed set of five
 * (`CALL_FAILURE`), and the hop's wire reasons are twelve: two of the five are
 * the answers the hop gives by name (a token that will not verify, a device that
 * is gone), the provider's own failure is the third, and **the rest are
 * `provider-error`** -- the call reached the hop and did not come back with an
 * answer, which is what that word is for. A policy refusal is the one mapping
 * worth stating: the badge's *policy* half is where a project's refusal is named
 * (`POLICY_REASON`, read from the status answer), and a call refused by policy
 * arriving at all means the app's status answer was stale or absent, so the
 * failure is recorded and the hop's own reason is carried on `detail` where a
 * reader of the run's record can see it.
 */
function callFailure(refusal) {
  switch (refusal.code) {
    case 'no-token':
      return CALL_FAILURE.EXPIRED_TOKEN;
    case 'unreachable':
    case 'bad-url':
      return CALL_FAILURE.UNREACHABLE;
    case 'refused':
      if (refusal.detail === 'token_unverified') return CALL_FAILURE.EXPIRED_TOKEN;
      if (refusal.detail === 'not_a_device' || refusal.detail === 'device_unknown') return CALL_FAILURE.REVOKED_DEVICE;
      return CALL_FAILURE.PROVIDER_ERROR;
    default:
      return CALL_FAILURE.PROVIDER_ERROR;
  }
}

/** The call record a badge reads, in `lastCallInDirection`'s exact shape. */
function callRecord({ direction, engine, provider, state, reason }) {
  return Object.freeze({ direction, engine, provider, state, reason });
}

/**
 * Build this account's egress.
 *
 * **One object per open account**, by the same rule `createVoiceSwitch` holds
 * (D114): the project list and the switch are one installation's, and an egress
 * that could speak for another installation's project would be rule 7 broken by
 * construction.
 *
 * @param {object} options
 * @param {object} options.hop the client from `./hop.js`; the **only** transport
 *   this module can send through, which is §5 rule 8's mechanism
 * @param {object} options.voiceSwitch the P5.3.6 switch; its `indicator` is what
 *   reads the device's setting and its recorded consent
 * @param {readonly string[]} options.projects the slugs this account may speak of
 * @param {readonly string[]} [options.secrets] the secrets the redactor masks on
 *   the text path (D118: the digest's secrets, and this app holds no provider
 *   key -- there is none on the device -- so this is the pairing material and
 *   whatever the account digest carries)
 * @param {(record: object) => void} [options.onCall] told when a remote call
 *   starts and when it ends, so a caller can render "in flight" and restore what
 *   it changed (§4.2, `.claude/rules/mobile.md`'s "restore the UI state"). It is
 *   told nothing for a local turn, because a local turn is the app's own engine
 *   and this module never sees it.
 */
export function createVoiceEgress({ hop, voiceSwitch, projects, secrets = [], onCall } = {}) {
  if (hop === null || typeof hop !== 'object') throw new TypeError('hop is the voice hop client');
  if (typeof hop.transcribe !== 'function' || typeof hop.speak !== 'function') {
    throw new TypeError('hop is the voice hop client: it has transcribe() and speak()');
  }
  if (voiceSwitch === null || typeof voiceSwitch !== 'object' || typeof voiceSwitch.indicator !== 'function') {
    throw new TypeError('voiceSwitch is the P5.3.6 switch: it has indicator()');
  }
  if (!Array.isArray(projects) || projects.some((slug) => typeof slug !== 'string')) {
    throw new TypeError('projects is the list of slugs this account may speak of');
  }
  if (!Array.isArray(secrets) || secrets.some((secret) => typeof secret !== 'string')) {
    throw new TypeError('secrets is the list the redactor masks');
  }
  if (onCall !== undefined && typeof onCall !== 'function') throw new TypeError('onCall is a function');
  const tell = onCall ?? (() => {});

  /** The badge for one turn, read from the record the device actually holds. */
  async function badgeFor({ direction, status, lastCallInDirection }) {
    const badge = await voiceSwitch.indicator({ direction, status, lastCallInDirection });
    if (badge !== null && typeof badge === 'object' && badge.ok !== undefined && badge.ok === false) {
      // The switch refused to render a badge -- it could not read its own record.
      // There is no consent answer and no setting answer, and a turn with neither
      // is local. The badge's refusal is carried as the reason so the caller can
      // show what went wrong rather than a generic "local".
      return Object.freeze({ ok: true, send: false, provider: null, reason: badge.code ?? REASON.NO_ANSWER });
    }
    return badge;
  }

  /**
   * One turn's decision, without sending anything.
   *
   * The screen calls this before it opens its local pipeline: what it answers is
   * where the turn would go and, when the local engine is the one that failed,
   * whether the app owes the user text entry.
   */
  async function decide({ direction, route, state, now, project, status, lastCallInDirection, localFailed = false } = {}) {
    const badge = await badgeFor({ direction, status, lastCallInDirection });
    const answer = permit({ direction, route, state, now, project, projects, badge });
    if (!answer.ok) return answer;
    if (answer.send) return answer;
    return local(direction, answer.reason, localFailed ? EGRESS_OFFER.TEXT_ENTRY : null);
  }

  /**
   * The one call, and the only one: §4.5's "nothing is retried against a second
   * provider", and rule 10's "nothing is queued".
   *
   * A failure is terminal and becomes a **local** answer with the failure in it,
   * which is the shape every row of §4.5 has. Nothing is written, timed, held or
   * re-attempted: the caller gets one record, and `onCall` is told the same
   * record twice -- once in flight, once finished -- so that a UI which changed
   * on the first can change back on the second.
   */
  async function send({ direction, decision, project, call, localFailed = false }) {
    const inFlight = callRecord({
      direction,
      engine: ENGINE.REMOTE,
      provider: decision.provider,
      state: CALL_STATE.IN_FLIGHT,
      reason: null,
    });
    tell(inFlight);
    const answer = await call();
    if (answer.ok) {
      const done = callRecord({
        direction,
        engine: ENGINE.REMOTE,
        provider: decision.provider,
        state: CALL_STATE.DONE,
        reason: null,
      });
      tell(done);
      return Object.freeze({ ...answer, direction, engine: ENGINE.REMOTE, provider: decision.provider, call: done });
    }
    const reason = callFailure(answer);
    const failed = callRecord({
      direction,
      engine: ENGINE.REMOTE,
      provider: decision.provider,
      state: CALL_STATE.FAILED,
      reason,
    });
    tell(failed);
    return Object.freeze({
      ok: true,
      send: false,
      engine: ENGINE.LOCAL,
      direction,
      provider: null,
      reason,
      offer: localFailed ? EGRESS_OFFER.TEXT_ENTRY : null,
      detail: answer.code ?? null,
      call: failed,
    });
  }

  return Object.freeze({
    decide,

    /**
     * One turn's audio (§5 rules 1 to 3, 5 to 10).
     *
     * `route` is the router's answer for this utterance -- from the local
     * engine's transcript when the device has one, which is the design's "the
     * local engine always runs when it is available" and is what makes the
     * destructive decision a decision about a transcript rather than about a
     * hope. On a device whose local engine cannot hear the user there is no such
     * transcript, `route` is `null`, and the turn is local: the residual §5 rule 3
     * states is that such a device's *first* destructive utterance may already
     * have been transcribed remotely, and this module cannot classify what it
     * cannot read, so it does not pretend to.
     */
    async transcribe({ audio, contentType, direction = 'stt', ...turn } = {}) {
      const decision = await decide({ direction, ...turn });
      if (!decision.ok) return decision;
      if (!decision.send) return decision;
      if (audio === undefined || audio === null) return refuse(REFUSAL.MISSING_FIELD, 'audio');
      return send({
        direction,
        decision,
        project: turn.project,
        localFailed: turn.localFailed ?? false,
        call: () => hop.transcribe({ project: turn.project, engine: decision.provider, audio, contentType }),
      });
    },

    /**
     * One turn's reply text (§5 rule 4 in particular).
     *
     * The redactor runs **here**, on the text, and what it returns is what goes.
     * It **substitutes and never drops** -- the message still leaves, with the
     * match replaced by `[REDACTED]` -- and a redactor that refused is a turn
     * served locally, because text whose redaction failed is text whose
     * redaction did not happen and §5 rule 4 makes the redactor's output the
     * thing that goes. Nothing here redacts audio: rule 4's own words are that
     * the rule "reaches text, which is the TTS and summariser path only", and
     * `transcribe` above applies no redactor at all.
     */
    async speak({ text, direction = 'tts', ...turn } = {}) {
      const decision = await decide({ direction, ...turn });
      if (!decision.ok) return decision;
      if (!decision.send) return decision;
      let outgoing;
      try {
        outgoing = redact(text, secrets);
      } catch {
        return local(direction, EGRESS_CODE.REDACTOR_FAILED, turn.localFailed ? EGRESS_OFFER.TEXT_ENTRY : null);
      }
      const answer = await send({
        direction,
        decision,
        project: turn.project,
        localFailed: turn.localFailed ?? false,
        call: () => hop.speak({ project: turn.project, engine: decision.provider, text: outgoing }),
      });
      if (!answer.ok || answer.send === false) return answer;
      return Object.freeze({ ...answer, text: outgoing, redacted: outgoing !== text });
    },
  });
}

/**
 * The read-back the app speaks for a pending action, rebuilt from the resolved
 * target (§5 rule 3, §6 point 1).
 *
 * It is here rather than only in `shared/confirm` so that this file's rule 3 and
 * the app's own speech are one call: the read-back is never the transcript, it
 * is never sent, and it is recomputed from `readBack(verb, target)` each time it
 * is spoken. `REDACTED` is re-exported beside it for the same reason -- a reader
 * of this file should be able to see that the marker the text path leaves is the
 * digest module's own and not a second spelling.
 */
export function readBackFor(verb, target) {
  return readBack(verb, target);
}

export { DESTRUCTIVE_VERBS, REDACTED };
