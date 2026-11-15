// The voice switch and its indicator, as one pure function (mobile plan, Task
// P5.3.4; decision D123 and D119; the design is
// ../../docs/mobile/design/voice-evaluation-switch.md, §4.2 and §4.5).
//
// One pure function of the four inputs the app actually holds:
//
//     indicator({ direction, settings, consent, status, call })
//       -> { ok: true, direction, engine, provider, state, inFlight, reason, text }
//       |  { ok: false, code, field }
//
// **Why one function and not two.** The switch decides where a direction's
// speech runs, and the indicator is the badge the design requires to be visible
// before anything is spoken (§4.2). They are one function because they are one
// answer read two ways: a module that decided the route and a second module that
// decided the badge could disagree, and the disagreement would be the badge
// claiming `local` while an utterance went to a provider -- the one failure this
// whole feature exists to prevent.
//
// **Why these four inputs, and nothing else** (§4.2): the app cannot read
// `config/policy/**` (the phone holds no grant on it, §2), so the project's half
// arrives only as the hop's project-status answer. The other three -- the two
// per-direction settings, the recorded consent, and the last call's route -- are
// what the device itself holds. The state is therefore a function of values and
// never of a policy tree, a clock, a network or a device, which is what makes it
// testable in CI with no phone, no microphone and no account (Global
// Constraint 10).
//
// **The rule, in the order the branches run. The order is the rule.**
//
//   1. an in-flight call in this direction   the badge is the call's: it says
//                                            `remote (<name>)` while a remote
//                                            call is running, whatever the
//                                            switch now says, because saying
//                                            `local` while audio leaves would
//                                            be a lie about the one thing the
//                                            badge exists for
//   2. a failed call in this direction       local, with the call's own failure
//                                            reason: the app answers from the
//                                            local engine (§4.5)
//   3. the switch is not `remote`            local. An unset switch is local,
//                                            on both directions
//   4. no consent recorded at all            local: remote is unreachable by
//                                            name, and the consent sheet is what
//                                            the app offers (§4.5)
//   5. the hop's answer is missing, or is    local -- checking. A guess is what
//      a query that could not answer         this state exists to avoid, and a
//                                            failed query is the same fact as
//                                            no answer: the project's effective
//                                            setting is not known
//   6. the hop refuses this direction        `blocked by project policy`, with
//                                            the hop's own reason. This is the
//                                            only source of that badge: the app
//                                            never reads the policy tree
//   7. no admitted name has consent          local: the device's half of the
//                                            gap, so the consent sheet is the
//                                            fix rather than the project
//   8. otherwise                             remote (<the first admitted name
//                                            the device has consented to>)
//
// Steps 1 and 2 are the last call's route; 3, 4 and 7 are the device's own
// values; 5 and 6 are the hop's answer. **Steps 3-8 run device-first**, and that
// order is this module's own reading of the plan's state table: when the device's
// half already answers local -- the switch is off, or no consent exists -- local
// is a fact about the device rather than a guess about the project, so it is
// neither `checking` nor `blocked`. A project that forces local is reported as
// `blocked` while the switch is on, which is when the user is asking for remote
// and the refusal is what they need to see.
//
// **Every failure lands on local, which is the one direction the whole table
// fails in.** The hop's own failures arrive on the status answer and are read by
// class: a query that could not answer (`query-failed`) is the same as no answer
// and is `local — checking`; an unreachable hop, an expired ID token and a
// revoked device node are answers the app has and are `local` with that reason
// (the app says which, and offers pairing again or the pairing screen -- §4.5).
// A call that failed folds into local with the call's reason, defaulting to
// `provider-error` for a remote call and `local-error` for a local one (the row
// where the local engine itself fails and the app offers text entry).
//
// **Two triples answer two questions, and the one case where they differ is
// deliberate.** `state`, `inFlight` and `text` are the badge: what the indicator
// shows right now. `engine`, `provider` and `reason` are the egress decision:
// where a new utterance in this direction goes, to whom, and why not remote --
// and they hold an invariant in every case, `engine` is `remote` exactly when
// `provider` is a name and `reason` is `null`. The two differ in one reachable
// case: a remote call in flight whose switch or project has since stopped
// admitting it. There the badge says `remote (<name>)` (it is happening) while
// `engine` is `local` and `provider` is `null` (nothing new may be sent). A
// caller that reads the egress triple before sending is therefore never told
// remote by anything but a live admissible decision, and `inFlight` is what
// stops it starting a second call (P5.3.7's egress path).
//
// **Nothing here reads a clock, a file, the network, a random source or a
// credential, and nothing here mutates what it is handed.** It is the switch and
// the badge and nothing else: the consent sheet, the settings storage, the hop
// client and the ten never-send rules of §5 are the app's (P5.3.6, P5.3.7), and
// the policy node, its reader and the hop that enforces it are P5.3.1-P5.3.3 and
// P5.3.5's. `shared/README.md` is the package's contract and
// `../../docs/mobile/implementation-plan.md`'s P5.3.4 is this module's task.

import { MAX_TIMESTAMP } from '../lease/index.js';

/**
 * The two directions, which are the two switches. The design offers the switch
 * per direction and not once (§3): remote STT sends raw audio and remote TTS
 * sends text that the redactor has already reached, so they are two privacy
 * propositions and the owner may answer them differently.
 */
export const DIRECTION = Object.freeze({ STT: 'stt', TTS: 'tts' });

/**
 * The two engines -- and the only two values either switch may hold. One
 * vocabulary for both, because a switch's value *is* the engine it names: a
 * second spelling would be a second thing to keep in step.
 */
export const ENGINE = Object.freeze({ LOCAL: 'local', REMOTE: 'remote' });

/**
 * What the badge shows. Nothing else is a state: a caller that renders an
 * unlisted string is rendering something this module never returns.
 *
 * `checking` is the design's `local — checking` (§4.2): the device's half allows
 * remote and the project's half is unknown, so the app serves locally and says
 * so rather than guessing in either direction.
 */
export const STATE = Object.freeze({
  LOCAL: 'local',
  CHECKING: 'checking',
  BLOCKED: 'blocked',
  REMOTE: 'remote',
});

/** The three states a call's route can be in. A route is not a badge. */
export const CALL_STATE = Object.freeze({
  IN_FLIGHT: 'in-flight',
  DONE: 'done',
  FAILED: 'failed',
});

/**
 * Why the hop refuses, on an answer it *did* give (§4.4 point 7 and §4.3): the
 * project forces local, it admits nothing in this direction, or the provider
 * chosen has no register row. These are the design's own three names, kebab-cased
 * here; the hop (P5.3.3, P5.3.5) answers with this vocabulary or maps to it.
 */
export const POLICY_REASON = Object.freeze({
  FORCE_LOCAL: 'force-local',
  NO_ADMISSION: 'no-admission',
  NO_REGISTER_ROW: 'no-register-row',
});

/**
 * Why the hop could not answer the project-status query at all, or why the app
 * knows the answer is unusable: an ID token that expired and could not be
 * refreshed, a device node revocation deleted, a hop that could not be reached,
 * a query that failed.
 *
 * They are read by class and not one by one: `query-failed` is the same fact as
 * no answer (`local — checking`), and the other three are answers the app holds
 * (`local`, with the reason, and the app says which).
 */
export const STATUS_FAILURE = Object.freeze({
  EXPIRED_TOKEN: 'expired-token',
  REVOKED_DEVICE: 'revoked-device',
  UNREACHABLE: 'unreachable',
  QUERY_FAILED: 'query-failed',
});

/**
 * Why the last call failed. It is the status set plus the two failures only a
 * call can have: a provider that returned an error (`provider-error`, §4.5's
 * "the provider is down, slow, or returns an error") and the local engine itself
 * failing (`local-error`, the row where the app offers text entry).
 */
export const CALL_FAILURE = Object.freeze({
  EXPIRED_TOKEN: 'expired-token',
  REVOKED_DEVICE: 'revoked-device',
  UNREACHABLE: 'unreachable',
  PROVIDER_ERROR: 'provider-error',
  LOCAL_ERROR: 'local-error',
});

/**
 * Why a new utterance in this direction is not remote -- or, for `blocked`, why
 * the project refuses it. The three device-level codes are this module's own;
 * every other member arrives on an input and is carried through by name, so the
 * app can tell an expired token from a project that forbids remote without
 * reading anything this module did not read.
 */
export const REASON = Object.freeze({
  /** The direction's switch is `local`, or unset. */
  SWITCH_LOCAL: 'switch-local',
  /** The device holds no consent for any provider a remote call could name. */
  NO_CONSENT: 'no-consent',
  /** There is no project-status answer yet: the badge is `local — checking`. */
  NO_ANSWER: 'no-answer',
  ...POLICY_REASON,
  ...STATUS_FAILURE,
  ...CALL_FAILURE,
});

/**
 * The refusal codes. One per distinct way an input can fail to be this
 * function's input, because "the badge showed local" and "the module was handed
 * something it could not read" are different facts and one code for both loses
 * the second. A refusal carries **no answer**: the caller keeps its own, and a
 * caller with no answer of its own serves locally, which is the direction every
 * failure in this module falls.
 *
 * The first six are spelled as `shared/confirm` spells them, so a reader of both
 * does not have to learn two vocabularies for the same failures.
 */
export const REFUSAL = Object.freeze({
  /** The value is not a plain object: null, an array, a primitive, an instance. */
  NOT_AN_OBJECT: 'not-an-object',
  /** A field this module does not name is present: the field set is the version. */
  UNKNOWN_FIELD: 'unknown-field',
  /** A required field is absent or explicitly undefined. */
  MISSING_FIELD: 'missing-field',
  /** A field read through a getter -- refused without ever calling it. */
  ACCESSOR_PROPERTY: 'accessor-property',
  /** An input that threw while being read (a revoked or throwing Proxy). */
  INTERNAL_ERROR: 'internal-error',
  /** A boolean field holds something that is not a boolean. */
  WRONG_TYPE: 'wrong-type',
  /** A `direction` that is neither `stt` nor `tts`. */
  UNKNOWN_DIRECTION: 'unknown-direction',
  /** A switch or a call's engine that is neither `local` nor `remote`. */
  UNKNOWN_ENGINE: 'unknown-engine',
  /** A call's `state` that is not one of the three. */
  UNKNOWN_CALL_STATE: 'unknown-call-state',
  /** A code outside the closed set of the field it arrived in. */
  UNKNOWN_CODE: 'unknown-code',
  /** A provider name that is not a name: empty, oversize, or URL-shaped. */
  BAD_PROVIDER: 'bad-provider',
  /** A consent's `at` that is not an epoch-millisecond integer in 1..TIME_MAX. */
  INVALID_AT: 'invalid-at',
  /** A `consent` that is not a plain array, or is longer than its cap. */
  INVALID_CONSENT: 'invalid-consent',
  /** Two consent records for one provider name. */
  DUPLICATE_CONSENT: 'duplicate-consent',
  /** A status answer's `providers` that is not a plain array, or is past its cap. */
  INVALID_PROVIDERS: 'invalid-providers',
  /** Two entries for one name in a status answer's `providers`. */
  DUPLICATE_PROVIDER: 'duplicate-provider',
  /** A call whose engine is local and which names a provider anyway. */
  UNEXPECTED_PROVIDER: 'unexpected-provider',
  /** A call that did not fail and carries a failure reason anyway. */
  UNEXPECTED_REASON: 'unexpected-reason',
});

/**
 * The longest provider name, and the most names a list may carry. Both are
 * bounds rather than measurements: a name is an operator's key in the policy
 * node and reaches a screen, and a list past the cap is a list this module
 * refuses rather than one it renders.
 */
export const PROVIDER_MAX = 64;
export const MAX_PROVIDERS = 64;
export const MAX_CONSENT = 64;

/**
 * What a provider name is: **a name, never a URL** (§4.3, "An entry is a
 * provider name, never a URL -- a policy-carried URL is a policy-carried
 * redirection"). One pattern settles empty, oversize, control characters, lone
 * surrogates, invisible format characters and a URL-shaped entry at once: `/`,
 * `:` and everything else outside `[A-Za-z0-9._-]` are refused, so `https://...`
 * cannot be a name and neither can a string that renders as one thing and reads
 * as another.
 */
const PROVIDER_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;

/** The two directions and the two engines as lists, for membership checks. */
const DIRECTIONS = Object.freeze([DIRECTION.STT, DIRECTION.TTS]);
const ENGINES = Object.freeze([ENGINE.LOCAL, ENGINE.REMOTE]);
const CALL_STATES = Object.freeze([CALL_STATE.IN_FLIGHT, CALL_STATE.DONE, CALL_STATE.FAILED]);
const POLICY_REASONS = Object.freeze(Object.values(POLICY_REASON));
const STATUS_FAILURES = Object.freeze(Object.values(STATUS_FAILURE));
const CALL_FAILURES = Object.freeze(Object.values(CALL_FAILURE));

/** The badge's own words, kept verbatim from the design (§4.2). */
const BADGE = Object.freeze({
  [STATE.LOCAL]: 'local',
  [STATE.CHECKING]: 'local — checking',
  [STATE.BLOCKED]: 'blocked by project policy',
});

/** What the badge adds while a call is in flight: the design's "it shows that". */
const IN_FLIGHT = ' — in flight';

/** The input's own fields. An unknown one is refused: this is not a bag. */
const INPUT_FIELDS = Object.freeze(['direction', 'settings', 'consent', 'status', 'call']);

/** The two switches, exactly. Both are optional, and both default to local. */
const SWITCH_FIELDS = Object.freeze(['stt', 'tts']);

/** A consent record: the provider, and the day the user agreed. */
const CONSENT_FIELDS = Object.freeze(['provider', 'at']);

/** An answer the hop gave about the policy. */
const STATUS_ANSWER_FIELDS = Object.freeze(['ok', 'providers', 'direction', 'reason']);

/** An answer the hop could not give. */
const STATUS_FAILED_FIELDS = Object.freeze(['ok', 'code']);

/** The status answer's per-direction map. */
const STATUS_DIRECTION_FIELDS = Object.freeze(['stt', 'tts']);

/** The last call's route. */
const CALL_FIELDS = Object.freeze(['direction', 'engine', 'provider', 'state', 'reason']);

/**
 * The switch, and the badge, as a function of the four inputs.
 *
 *     indicator({
 *       direction: 'stt' | 'tts',
 *       settings?: { stt?: 'local' | 'remote', tts?: 'local' | 'remote' },
 *       consent?: [{ provider: 'name', at: 1700000000000 }, ...],
 *       status?: { ok: true, providers?: ['name', ...],
 *                  direction?: { stt?: boolean, tts?: boolean },
 *                  reason?: 'force-local' | 'no-admission' | 'no-register-row' }
 *              | { ok: false, code: 'expired-token' | 'revoked-device'
 *                                | 'unreachable' | 'query-failed' }
 *              | null,
 *       call?: { direction: 'stt', engine: 'local' | 'remote',
 *                provider: 'name' | null, state: 'in-flight' | 'done' | 'failed',
 *                reason: 'provider-error' | ... | null } | null,
 *     })
 *
 * An absent field is its default, and every default is the local, fail-closed
 * one: an unset switch is local, no consent is no consent, no status is
 * `local — checking`, and no call is a call that never happened. A field that is
 * **present and unreadable** is a refusal rather than a default, because a
 * value this module could not read is a bug in the caller rather than a fact
 * about the world.
 *
 * It returns the answer above, frozen, or `{ ok: false, code, field }`. The
 * egress triple holds one invariant in every answer: `engine` is `remote` exactly
 * when `provider` is a name and `reason` is `null`. It never throws -- an
 * exception from a hostile input (a revoked or throwing Proxy) is answered as
 * `internal-error` -- never coerces a wrong type into a right one, and never
 * mutates its input.
 *
 * @param {object} input the four inputs, and the direction the question is about
 * @returns {{ ok: true, direction: string, engine: string, provider: string | null,
 *             state: string, inFlight: boolean, reason: string | null, text: string }
 *           | { ok: false, code: string, field: string | null }}
 */
export function indicator(input) {
  try {
    return indicatorUnguarded(input);
  } catch {
    // A Proxy can throw from any trap, and a revoked one throws from all of
    // them. That is a refusal like any other, and what was thrown is never
    // echoed: it may carry whatever the input put in it.
    return refuse(REFUSAL.INTERNAL_ERROR, null);
  }
}

function indicatorUnguarded(input) {
  // Every input is read and refused **before** any answer is chosen, so a caller
  // hears the reason about its own value rather than about the state it happens
  // to be in -- and so no half-read input can reach a branch.
  const top = readFields(input, INPUT_FIELDS, 'input');
  if (!top.ok) return top;
  const values = top.values;

  // 0. The direction the question is about. It is not one of the four inputs:
  // it is which of the two switches is being read.
  const direction = values.direction;
  if (direction === undefined) return refuse(REFUSAL.MISSING_FIELD, 'direction');
  if (typeof direction !== 'string' || !DIRECTIONS.includes(direction)) {
    return refuse(REFUSAL.UNKNOWN_DIRECTION, 'direction');
  }

  const switches = readSwitches(values.settings);
  if (!switches.ok) return switches;

  const consent = readConsent(values.consent);
  if (!consent.ok) return consent;

  const call = readCall(values.call);
  if (!call.ok) return call;

  const status = readStatus(values.status);
  if (!status.ok) return status;

  const decision = decide(direction, switches.values, consent.values, status.values);
  const last = call.values !== null && call.values.direction === direction ? call.values : null;

  return answer(direction, overlay(decision, last));
}

// ---------------------------------------------------------------------------
// The decision: where a new utterance in this direction goes
// ---------------------------------------------------------------------------

/**
 * decide answers the egress question, and it is the fail-closed half of this
 * module: `engine` is `remote` only when the switch, the consent and the hop's
 * own answer all admit it. The branches are the header's list, steps 3-8.
 *
 * It returns `{ engine, provider, reason, state }`: `state` is the badge the
 * decision asks for, and `provider` is the name that badge would carry. The last
 * call's route is applied afterwards, by `overlay`.
 */
function decide(direction, switches, consented, status) {
  // 3. An unset switch is local, on both directions.
  if (switches[direction] !== ENGINE.REMOTE) return local(REASON.SWITCH_LOCAL);

  // 4. Remote is reachable only with a recorded consent for the name it would
  // use, and with none at all there is no name to try: the consent sheet is what
  // the app offers, and it is the device's own half of the gap.
  if (consented.length === 0) return local(REASON.NO_CONSENT);

  // 5. The project's effective setting is unknown: no answer yet, or a query
  // that could not answer. The app serves locally and says it is checking, and
  // it never guesses -- in either direction.
  if (status === null) return checking(REASON.NO_ANSWER);

  // The hop's own failures, read by class. A query that could not answer is the
  // same fact as no answer; an unreachable hop, an expired token and a revoked
  // device are answers the app holds, so they are local with their reason rather
  // than a state that says "still asking".
  if (status.ok === false) {
    if (status.code === STATUS_FAILURE.QUERY_FAILED) return checking(status.code);
    return local(status.code);
  }

  // 6. The hop answered about the policy, and it refuses this direction: this is
  // the only source of `blocked by project policy`, because the app cannot read
  // the policy tree and does not try to. An answer that admits nothing names its
  // reason; one that does not say why is `no-admission`, the default deny §4.3
  // is built on.
  if (status.direction[direction] !== true || status.providers.length === 0) {
    return blocked(status.reason === null ? POLICY_REASON.NO_ADMISSION : status.reason);
  }

  // 7 and 8. A name must be both admitted by the project and consented to on the
  // device. When the two sets do not meet, the state is local with `no-consent`
  // and not `blocked`: the project admitted remote, and what is missing is the
  // device's own consent for a name it does admit -- so the consent sheet is the
  // fix, and `blocked` would blame the project for a device that is out of step.
  // The first admitted name with consent is the one a call names, in the hop's
  // own order, so the badge is deterministic and the operator's order is kept.
  const provider = status.providers.find((name) => consented.includes(name));
  if (provider === undefined) return local(REASON.NO_CONSENT);

  return remote(provider);
}

/** Local: the engine the whole table falls to, with the one reason it is there. */
function local(reason) {
  return Object.freeze({ engine: ENGINE.LOCAL, provider: null, reason, state: STATE.LOCAL });
}

/**
 * Checking: local, and the badge says the project's setting is unknown. It is
 * `local` for the engine, because that is the direction it serves in -- the
 * state is about what the app can say, not about what it does.
 */
function checking(reason) {
  return Object.freeze({ engine: ENGINE.LOCAL, provider: null, reason, state: STATE.CHECKING });
}

/** Blocked: the hop refused this direction, and the badge says so. */
function blocked(reason) {
  return Object.freeze({ engine: ENGINE.LOCAL, provider: null, reason, state: STATE.BLOCKED });
}

/** Remote: the one state in which an utterance leaves the device. */
function remote(provider) {
  return Object.freeze({ engine: ENGINE.REMOTE, provider, reason: null, state: STATE.REMOTE });
}

// ---------------------------------------------------------------------------
// The badge, and the last call's route
// ---------------------------------------------------------------------------

/**
 * overlay applies the last call's route to the decision, and returns the whole
 * answer but the text: `{ engine, provider, state, inFlight, reason, badgeName }`.
 *
 *   * **a call in flight is what the badge shows**, whatever the decision says,
 *     because the badge's whole job is to be honest about speech that is leaving
 *     now: a switch turned off mid-call must not make the indicator say `local`
 *     while the audio is still going out. The **decision is untouched**: `engine`,
 *     `provider` and `reason` still answer "where would a new utterance go, and
 *     to whom", so a caller that reads them before sending is never told remote
 *     by a call it must not start -- `inFlight` is what tells it not to start one.
 *   * **a failed call is local**, with the call's own failure reason (§4.5: the
 *     app answers from the local engine and the indicator shows `local`). The
 *     engine, the provider and the reason are all the local fallback's, and this
 *     is the one place the route changes them.
 *   * **a call that finished changes nothing**: the answer is the decision the
 *     four inputs give, which for a completed remote call is still remote -- the
 *     switch is what it was.
 *
 * `badgeName` is the name the badge shows, and it is separate from `provider` so
 * that the egress triple keeps its invariant in every case: `engine` is `remote`
 * exactly when `provider` is a name and `reason` is `null`, always. While a
 * remote call is in flight and the decision has since gone local, the badge still
 * names the provider the audio is actually going to (`badgeName`) while
 * `provider` stays `null` -- the badge tells the truth about now, and the egress
 * answer stays fail-closed.
 */
function overlay(decision, last) {
  if (last === null || last.state === CALL_STATE.DONE) {
    return Object.freeze({
      engine: decision.engine,
      provider: decision.provider,
      state: decision.state,
      inFlight: false,
      reason: decision.reason,
      badgeName: decision.state === STATE.REMOTE ? decision.provider : null,
    });
  }
  if (last.state === CALL_STATE.IN_FLIGHT) {
    const going = last.engine === ENGINE.REMOTE;
    return Object.freeze({
      engine: decision.engine,
      provider: decision.provider,
      state: going ? STATE.REMOTE : STATE.LOCAL,
      inFlight: true,
      reason: decision.reason,
      badgeName: going ? last.provider : null,
    });
  }
  return Object.freeze({
    engine: ENGINE.LOCAL,
    provider: null,
    state: STATE.LOCAL,
    inFlight: false,
    reason: failureReason(last),
    badgeName: null,
  });
}

/** The failure a failed call's answer carries, named or defaulted. */
function failureReason(last) {
  if (last.reason !== null) return last.reason;
  return last.engine === ENGINE.REMOTE ? CALL_FAILURE.PROVIDER_ERROR : CALL_FAILURE.LOCAL_ERROR;
}

/**
 * answer assembles the frozen record, and the badge's text.
 *
 * `text` is the design's own wording, verbatim (§4.2: `local`,
 * `remote (<provider name>)`, `blocked by project policy`, and `local —
 * checking`), because the words the badge uses are part of what was designed and
 * not a rendering detail a caller may rewrite. While a call is in flight the text
 * says so, which is the design's "while a call is in flight it shows that".
 *
 * `provider` is the name an utterance would be sent to and `text` is what the
 * badge reads; the two names differ in exactly one case (a remote call in flight
 * whose decision has gone local), and each is documented at `overlay`.
 */
function answer(direction, badge) {
  const text = badge.state === STATE.REMOTE ? `remote (${badge.badgeName})` : BADGE[badge.state];
  return Object.freeze({
    ok: true,
    direction,
    engine: badge.engine,
    provider: badge.provider,
    state: badge.state,
    inFlight: badge.inFlight,
    reason: badge.reason,
    text: badge.inFlight ? `${text}${IN_FLIGHT}` : text,
  });
}

// ---------------------------------------------------------------------------
// The input's readers
// ---------------------------------------------------------------------------

/**
 * readFields reads exactly the named fields into a null-prototype snapshot, each
 * field once, through its own descriptor.
 *
 * A value that is not a plain data object is refused (`not-an-object`), and a key
 * this module does not name -- a symbol included -- is refused (`unknown-field`),
 * because the field set is the version: a value from a later protocol must be
 * refused rather than read with its new field dropped. An accessor property is
 * refused (`accessor-property`) without ever being called, because a getter can
 * answer differently each time it is read, and this module's whole claim is that
 * its answer is a function of the value it was handed.
 *
 * An unknown key is reported by `where` -- the container this module named -- and
 * never by the key itself: the key is the input's choice, and a refusal that
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
 * readSwitches reads the two settings. An absent map is both switches unset, and
 * an absent switch is `local` -- the design's "an unset switch is local" is the
 * default here rather than a rule the caller has to remember.
 */
function readSwitches(value) {
  if (value === undefined || value === null) {
    return { ok: true, values: Object.freeze({ [DIRECTION.STT]: ENGINE.LOCAL, [DIRECTION.TTS]: ENGINE.LOCAL }) };
  }
  const read = readFields(value, SWITCH_FIELDS, 'settings');
  if (!read.ok) return read;

  const switches = { [DIRECTION.STT]: ENGINE.LOCAL, [DIRECTION.TTS]: ENGINE.LOCAL };
  for (const direction of DIRECTIONS) {
    const setting = read.values[direction];
    if (setting === undefined) continue;
    if (typeof setting !== 'string' || !ENGINES.includes(setting)) {
      return refuse(REFUSAL.UNKNOWN_ENGINE, `settings.${direction}`);
    }
    switches[direction] = setting;
  }
  return { ok: true, values: Object.freeze(switches) };
}

/**
 * readConsent reads what the user has agreed to, per provider name: a plain
 * array of `{ provider, at }` records, and the names it yields.
 *
 * The time is validated and then dropped rather than carried: a consent is a
 * dated act -- the consent sheet shows the day it was given -- and one recorded
 * without a date is a claim with no date, which this module refuses rather than
 * treats as a flag. Two records for one name are refused too: a set that can be
 * read two ways would have two answers, and it is a caller bug either way.
 */
function readConsent(value) {
  if (value === undefined || value === null) return { ok: true, values: Object.freeze([]) };
  if (!Array.isArray(value) || Object.getPrototypeOf(value) !== Array.prototype) {
    return refuse(REFUSAL.INVALID_CONSENT, 'consent');
  }
  const length = Object.getOwnPropertyDescriptor(value, 'length').value;
  if (length > MAX_CONSENT) return refuse(REFUSAL.INVALID_CONSENT, 'consent');

  const names = [];
  const seen = new Set();
  for (let index = 0; index < length; index += 1) {
    const descriptor = Object.getOwnPropertyDescriptor(value, String(index));
    if (descriptor === undefined || !Object.hasOwn(descriptor, 'value')) {
      return refuse(REFUSAL.INVALID_CONSENT, 'consent');
    }
    const read = readFields(descriptor.value, CONSENT_FIELDS, 'consent');
    if (!read.ok) return read;

    const provider = read.values.provider;
    if (provider === undefined) return refuse(REFUSAL.MISSING_FIELD, 'consent.provider');
    if (!isProvider(provider)) return refuse(REFUSAL.BAD_PROVIDER, 'consent.provider');

    const at = read.values.at;
    if (at === undefined) return refuse(REFUSAL.MISSING_FIELD, 'consent.at');
    if (!isTimestamp(at)) return refuse(REFUSAL.INVALID_AT, 'consent.at');

    if (seen.has(provider)) return refuse(REFUSAL.DUPLICATE_CONSENT, 'consent.provider');
    seen.add(provider);
    names.push(provider);
  }
  return { ok: true, values: Object.freeze(names) };
}

/**
 * readCall reads the last call's route: `{ direction, engine, provider, state,
 * reason }`.
 *
 * It is read strictly because it is the one input the app writes from a live
 * call, and two of its combinations cannot be true: a call whose engine is local
 * and which names a provider (a local call has nothing to name), and a call that
 * did not fail and yet carries a failure reason. Both are refused rather than
 * ignored, because a record that says one thing and means another is how a badge
 * comes to describe a call that never happened.
 *
 * A remote call **must** name its provider: `remote` with no name would render as
 * `remote (null)`, and a badge that cannot say who the audio went to is the one
 * thing the indicator must never be.
 */
function readCall(value) {
  if (value === undefined || value === null) return { ok: true, values: null };
  const read = readFields(value, CALL_FIELDS, 'call');
  if (!read.ok) return read;
  const call = read.values;

  const direction = call.direction;
  if (direction === undefined) return refuse(REFUSAL.MISSING_FIELD, 'call.direction');
  if (typeof direction !== 'string' || !DIRECTIONS.includes(direction)) {
    return refuse(REFUSAL.UNKNOWN_DIRECTION, 'call.direction');
  }

  const engine = call.engine;
  if (engine === undefined) return refuse(REFUSAL.MISSING_FIELD, 'call.engine');
  if (typeof engine !== 'string' || !ENGINES.includes(engine)) {
    return refuse(REFUSAL.UNKNOWN_ENGINE, 'call.engine');
  }

  const state = call.state;
  if (state === undefined) return refuse(REFUSAL.MISSING_FIELD, 'call.state');
  if (typeof state !== 'string' || !CALL_STATES.includes(state)) {
    return refuse(REFUSAL.UNKNOWN_CALL_STATE, 'call.state');
  }

  const rawProvider = call.provider;
  const provider = rawProvider === undefined ? null : rawProvider;
  if (provider !== null && !isProvider(provider)) return refuse(REFUSAL.BAD_PROVIDER, 'call.provider');
  if (engine === ENGINE.REMOTE && provider === null) {
    return refuse(REFUSAL.MISSING_FIELD, 'call.provider');
  }
  if (engine === ENGINE.LOCAL && provider !== null) {
    return refuse(REFUSAL.UNEXPECTED_PROVIDER, 'call.provider');
  }

  const rawReason = call.reason;
  const reason = rawReason === undefined ? null : rawReason;
  if (reason !== null && !CALL_FAILURES.includes(reason)) {
    return refuse(REFUSAL.UNKNOWN_CODE, 'call.reason');
  }
  if (state !== CALL_STATE.FAILED && reason !== null) {
    return refuse(REFUSAL.UNEXPECTED_REASON, 'call.reason');
  }

  return { ok: true, values: Object.freeze({ direction, engine, provider, state, reason }) };
}

/**
 * readStatus reads the hop's last project-status answer, in its two shapes,
 * discriminated by `ok`.
 *
 *   * `{ ok: true, providers, direction, reason }` -- an answer about the policy:
 *     the names admitted, the directions admitted, and why something is refused.
 *   * `{ ok: false, code }` -- the hop could not answer, and names why.
 *
 * Both shapes have an **exact** field set, and the set is chosen by `ok` the way
 * `shared/confirm` chooses an event's set by its `type`: a field that belongs to
 * the other shape is an `unknown-field` rather than a value this module reads
 * past. An answer with no `ok` at all is a missing field, and one whose `ok` is
 * not a boolean is a `wrong-type` -- the refusal must be about the shape of the
 * answer, not about the policy the answer would have described.
 */
function readStatus(value) {
  if (value === undefined || value === null) return { ok: true, values: null };
  if (!isPlainObject(value)) return refuse(REFUSAL.NOT_AN_OBJECT, 'status');

  const descriptor = Object.getOwnPropertyDescriptor(value, 'ok');
  if (descriptor === undefined) return refuse(REFUSAL.MISSING_FIELD, 'status.ok');
  if (!Object.hasOwn(descriptor, 'value')) return refuse(REFUSAL.ACCESSOR_PROPERTY, 'status');
  if (typeof descriptor.value !== 'boolean') return refuse(REFUSAL.WRONG_TYPE, 'status.ok');

  return descriptor.value === false ? readStatusFailure(value) : readStatusAnswer(value);
}

/** The hop could not answer: exactly one code, from the closed set. */
function readStatusFailure(value) {
  const read = readFields(value, STATUS_FAILED_FIELDS, 'status');
  if (!read.ok) return read;
  const code = read.values.code;
  if (code === undefined) return refuse(REFUSAL.MISSING_FIELD, 'status.code');
  if (typeof code !== 'string' || !STATUS_FAILURES.includes(code)) {
    return refuse(REFUSAL.UNKNOWN_CODE, 'status.code');
  }
  return { ok: true, values: Object.freeze({ ok: false, code }) };
}

/**
 * The hop answered about the policy. `providers` and `direction` are optional and
 * both default to the deny §4.3 is built on: no names, no directions. An
 * unreadable policy node arrives here as an answer that admits nothing, which is
 * §4.3's "absent, empty, malformed or unreadable all mean local only" -- the four
 * answers are one answer to a caller that must not guess.
 */
function readStatusAnswer(value) {
  const read = readFields(value, STATUS_ANSWER_FIELDS, 'status');
  if (!read.ok) return read;

  const providers = readProviders(read.values.providers);
  if (!providers.ok) return providers;
  const direction = readStatusDirection(read.values.direction);
  if (!direction.ok) return direction;

  const rawReason = read.values.reason;
  const reason = rawReason === undefined ? null : rawReason;
  if (reason !== null && !POLICY_REASONS.includes(reason)) {
    return refuse(REFUSAL.UNKNOWN_CODE, 'status.reason');
  }

  return { ok: true, values: Object.freeze({ ok: true, providers: providers.values, direction: direction.values, reason }) };
}

/** The admitted names: a plain array of names, bounded, one row per name. */
function readProviders(value) {
  if (value === undefined || value === null) return { ok: true, values: Object.freeze([]) };
  if (!Array.isArray(value) || Object.getPrototypeOf(value) !== Array.prototype) {
    return refuse(REFUSAL.INVALID_PROVIDERS, 'status.providers');
  }
  const length = Object.getOwnPropertyDescriptor(value, 'length').value;
  if (length > MAX_PROVIDERS) return refuse(REFUSAL.INVALID_PROVIDERS, 'status.providers');

  const names = [];
  const seen = new Set();
  for (let index = 0; index < length; index += 1) {
    const descriptor = Object.getOwnPropertyDescriptor(value, String(index));
    if (descriptor === undefined || !Object.hasOwn(descriptor, 'value')) {
      return refuse(REFUSAL.INVALID_PROVIDERS, 'status.providers');
    }
    const name = descriptor.value;
    if (!isProvider(name)) return refuse(REFUSAL.BAD_PROVIDER, 'status.providers');
    if (seen.has(name)) return refuse(REFUSAL.DUPLICATE_PROVIDER, 'status.providers');
    seen.add(name);
    names.push(name);
  }
  return { ok: true, values: Object.freeze(names) };
}

/** The directions the project admits: two booleans, and nothing else. */
function readStatusDirection(value) {
  const both = { [DIRECTION.STT]: false, [DIRECTION.TTS]: false };
  if (value === undefined || value === null) return { ok: true, values: Object.freeze(both) };
  const read = readFields(value, STATUS_DIRECTION_FIELDS, 'status.direction');
  if (!read.ok) return read;

  for (const direction of DIRECTIONS) {
    const admitted = read.values[direction];
    if (admitted === undefined) continue;
    if (typeof admitted !== 'boolean') return refuse(REFUSAL.WRONG_TYPE, `status.direction.${direction}`);
    both[direction] = admitted;
  }
  return { ok: true, values: Object.freeze(both) };
}

// ---------------------------------------------------------------------------
// The small predicates
// ---------------------------------------------------------------------------

/** A name, never a URL, never empty, never past its bound. */
function isProvider(value) {
  return typeof value === 'string' && value.length <= PROVIDER_MAX && PROVIDER_PATTERN.test(value);
}

/**
 * An integer inside 1..TIME_MAX, and never a negative zero.
 *
 * `-0` is the shape of a number that has been negated -- a computation's residue
 * rather than a time -- and the bound is `shared/lease`'s, imported, because two
 * modules must not disagree about what a time is.
 */
function isTimestamp(value) {
  return (
    typeof value === 'number' &&
    Number.isSafeInteger(value) &&
    !Object.is(value, -0) &&
    value >= 1 &&
    value <= MAX_TIMESTAMP
  );
}

/** A plain data object, and not an instance of anything. */
function isPlainObject(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

// ---------------------------------------------------------------------------
// The answers
// ---------------------------------------------------------------------------

/** A refusal: no badge, a code the caller can branch on, and the field at fault. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}
