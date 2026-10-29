// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The voice session's event contract, as the spec's §5 writes it (voice-session
// plan, Task VS0.1; D205, D206, D213, F3, F9, P8).
//
// **What this module is.** `mobile/src/voice-session/` owns the *session*: is
// there one, is it paused and why, and what ended it. The *turn* -- whose words
// these are, whether an utterance is in flight -- stays with P3.4's pipeline
// (`mobile/src/audio/pipeline.js`), and this module never stores a copy of it
// (F3, D206). The two meet at one stream: the native module emits the events
// below, the machine in `machine.js` reduces them, and the controller (VS1.1,
// not written yet) drives the pipeline from the answer.
//
// **The contract is frozen here and nowhere else.** Both platforms emit the
// *same* six kinds (§5: "Native → JS events, identical on both platforms"), so
// the kinds, the routes, the states and the reasons are named once, as frozen
// values, in the shape this tree already uses for a port's vocabulary:
// `PIPELINE_EVENTS`/`AUDIO_IN_EVENTS` are frozen arrays of names,
// `PIPELINE_EVENT_FIELDS` is a frozen map of an event's exact fields,
// `AUDIO_IN_CODES` and `INTERRUPT_SWITCH` are frozen objects of named values.
// A caller that spelled one of these strings by hand could spell it wrong, and
// nothing would notice until a phone behaved differently from the other one.
//
// **One field is this contract's, and it is P8's.** §5 writes
// `interruptionBegan` as `{ source }`, and that is not enough: iOS delivers an
// interruption notification when it *suspends* an app and the app runs again --
// `AVAudioSessionInterruptionWasSuspendedKey` is in the dictionary -- and that
// notification "is necessarily delayed in time" and is not an interruption at
// all (platform fact P8). A design that cannot tell the two apart reports a
// call that never happened, so `interruptionBegan` carries **`suspended`**, and
// `machine.js` treats a `suspended` interruption as no interruption. The field
// is **optional**: absent means what §5 means by the event (a real
// interruption), and the iOS half of the native module sends `true` when the
// notification carried the key. Where the spec and the reconciliation disagree
// the reconciliation wins, and this is the one place they do.
//
// **What the contract deliberately cannot carry.** No audio, no transcript and
// no credential: the fields below are a kind, a source, a boolean, a route, an
// action, a reason, a code and a message -- and `index.test.js` scans this
// directory for the tokens a transcript or a sample would need (F9, D213). What
// the Live Activity and the Android notification are updated with is the
// machine's state: a handful of scalars (a state name, a boolean, a timestamp,
// a title), never words the user said (F25).
//
// **Why a validator and not a judgement at the transition.** `isVoicePipeline`
// is the precedent: a port's shape is checked where it is handed over, so the
// mistake costs one refusal at the boundary instead of a wrong transition deep
// inside. `isVoiceSessionEvent` is the same check for one event, and the native
// port (VS1.2) refuses a malformed event with it before anything reduces it.

/**
 * The six event kinds, exactly as §5 writes them. Nothing else is an event.
 *
 * A type outside this list is not a session event at all; the machine refuses
 * it (`MACHINE_CODES.UNKNOWN_EVENT`) rather than guessing.
 */
export const SESSION_EVENTS = Object.freeze([
  'interruptionBegan',
  'interruptionEnded',
  'routeChanged',
  'remoteAction',
  'serviceStopped',
  'error',
]);

/**
 * The exact fields of each kind, as a frozen map -- `PIPELINE_EVENT_FIELDS`'
 * shape, one level up.
 *
 * This is the contract's own fence: the tests walk every event they build and
 * assert its fields are exactly these, so a field added later is a deliberate
 * edit to this map rather than a key that quietly rides along. The one optional
 * field is `interruptionBegan.suspended` (see the header); every other field
 * listed here is required, and the validator below refuses an event that is
 * missing one.
 */
export const SESSION_EVENT_FIELDS = Object.freeze({
  /** P8: `suspended` is present only when the OS said the app was suspended. */
  interruptionBegan: Object.freeze(['type', 'source', 'suspended']),
  /** §6.2: the OS's own `shouldResume`, never a guess -- see §4's transitions. */
  interruptionEnded: Object.freeze(['type', 'shouldResume']),
  /** §6.3/§7.5: the new route, and whether continuing on it is unsafe. */
  routeChanged: Object.freeze(['type', 'route', 'unsafe']),
  /** §6.5/§7.2: a control pressed outside the app (Live Activity, notification). */
  remoteAction: Object.freeze(['type', 'action']),
  /** §9: the platform stopped the service this session was running in. */
  serviceStopped: Object.freeze(['type', 'reason']),
  /** §10: something went wrong; `code` is the emitter's own vocabulary. */
  error: Object.freeze(['type', 'code', 'message']),
});

/**
 * The routes §5 names. `unknown` is a route the platform would not name, and it
 * is a value rather than an absence -- a route change to an unnamed route is
 * still a route change.
 */
export const AUDIO_ROUTES = Object.freeze(['speaker', 'receiver', 'wired', 'bluetooth', 'usb', 'unknown']);

/**
 * The eight states of §4, in the order the spec's table writes them. The machine
 * in `machine.js` produces these and no others.
 */
export const SESSION_STATES = Object.freeze([
  'idle',
  'starting',
  'listening',
  'speaking',
  'paused_interruption',
  'paused_route',
  'ending',
  'ended',
]);

/**
 * §5's `interruptionBegan.source`: what took the audio. A call is the case §11's
 * matrix is written around; `system` is Siri, an alarm or a timer (§10), and
 * `other_app` is another app taking audio focus (§7.3).
 */
export const INTERRUPTION_SOURCES = Object.freeze({
  CALL: 'call',
  SYSTEM: 'system',
  OTHER_APP: 'other_app',
});

/** §5's `remoteAction.action`: the three controls that exist outside the app. */
export const REMOTE_ACTIONS = Object.freeze({
  MUTE: 'mute',
  UNMUTE: 'unmute',
  HANG_UP: 'hangUp',
});

/** §5's `serviceStopped.reason`: who or what stopped the service (§9, P22). */
export const SERVICE_STOP_REASONS = Object.freeze({
  SYSTEM: 'system',
  USER: 'user',
  ERROR: 'error',
});

/**
 * The two reasons a session is paused, and there are exactly two because §4 has
 * exactly two paused states. The *detail* is the event's own -- an interruption
 * pause carries `interruptionBegan.source`, a route pause carries the route --
 * so "why is it paused" is answered by the event that paused it and not by a
 * second flag maintained beside it.
 */
export const PAUSE_REASONS = Object.freeze({
  INTERRUPTION: 'interruption',
  ROUTE: 'route',
});

/**
 * The reasons a session ends, as a closed set.
 *
 * `interruption_timeout` is §4's and §8's: the timeout fired, or the
 * interruption ended with `shouldResume: false` (the spec gives that path the
 * same reason, and `machine.js` keeps the spec's reading -- see its header).
 * `permission` is §10's, for a permission revoked mid-session. `hangup` is §4's
 * hang-up from the app, the Live Activity or the notification. `error` and
 * `service_stopped` are §5's two events that leave no session behind.
 */
export const END_REASONS = Object.freeze({
  HANGUP: 'hangup',
  INTERRUPTION_TIMEOUT: 'interruption_timeout',
  PERMISSION: 'permission',
  ERROR: 'error',
  SERVICE_STOPPED: 'service_stopped',
});

/** Whether `value` is one of a frozen vocabulary's values. */
function isOneOf(vocabulary, value) {
  return Object.values(vocabulary).includes(value);
}

/**
 * Whether `value` is a well-formed session event: one of §5's six kinds, with
 * exactly that kind's fields, each holding a value this contract has.
 *
 * A shape check at the boundary, deliberately shallow in the way
 * `isVoicePipeline` is: it promises nothing about whether the event makes
 * sense *now* -- that is the machine's transition table, and the answer there is
 * a refusal with its own code. What it promises is that nothing downstream has
 * to ask whether `route` is a route, and that **every** input gets an answer:
 * `true` or `false`, never a throw, however odd the platform's `type` string is
 * (see `Object.hasOwn` below).
 *
 * The three refusals the plan names are the ones a native module is most likely
 * to get wrong: an unknown `type` (a platform emitting its own event), an
 * unknown `route` (a platform's own route name), and a missing `shouldResume`
 * (the field §6.2 says must always be honoured, so an emitter that forgets it
 * is an emitter that would have the app resume on `.ended` alone).
 *
 * @param {unknown} value a candidate event
 * @returns {boolean}
 */
export function isVoiceSessionEvent(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return false;
  // `Object.hasOwn` and not `SESSION_EVENT_FIELDS[value.type]`: a `type` that
  // names an `Object.prototype` member -- `constructor`, `toString`, `valueOf`,
  // `hasOwnProperty`, `__proto__` -- would otherwise resolve to an *inherited*
  // value, and the field check below would throw on it. A validator a native
  // port refuses events with (VS1.2) answers yes or no to whatever a platform
  // sends, and a string a platform happened to choose is not allowed to turn a
  // refusal into a crash.
  if (!Object.hasOwn(SESSION_EVENT_FIELDS, value.type)) return false;
  const fields = SESSION_EVENT_FIELDS[value.type];
  if (Object.keys(value).some((key) => !fields.includes(key))) return false;
  switch (value.type) {
    case 'interruptionBegan':
      // `suspended` is the one optional field, and a value that is not a
      // boolean is not an answer to "was this the suspension notification".
      return (
        isOneOf(INTERRUPTION_SOURCES, value.source) &&
        (value.suspended === undefined || typeof value.suspended === 'boolean')
      );
    case 'interruptionEnded':
      return typeof value.shouldResume === 'boolean';
    case 'routeChanged':
      return AUDIO_ROUTES.includes(value.route) && typeof value.unsafe === 'boolean';
    case 'remoteAction':
      return isOneOf(REMOTE_ACTIONS, value.action);
    case 'serviceStopped':
      return isOneOf(SERVICE_STOP_REASONS, value.reason);
    case 'error':
      // §5's shape: a code and a message. The code is the emitter's own
      // vocabulary and is not judged here -- the machine judges the one code it
      // knows (`END_REASONS.PERMISSION`) and treats every other as `error`.
      return typeof value.code === 'string' && value.code.length > 0 && typeof value.message === 'string';
    default:
      return false;
  }
}
