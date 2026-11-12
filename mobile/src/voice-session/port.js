// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// §5's native port: its vocabulary, its shape, and the event half every port
// carries (voice-session plan, Task VS1.2; D213, D205, P8).
//
// **What a port is, and why this one is a file rather than a module.** P3.4's
// three ports are the precedent and this is the same shape one level up: §5
// names what JS asks native for (`start`, `updateStatus`, `setMuted`, `end`,
// `cleanupStaleSessions`) and what native tells JS (the six kinds of
// `events.js`), a phone is what answers, and the code that is written before a
// phone exists is written against **that** instead of against a device. So what
// lives here is the contract both platform halves are held to -- the method
// list, the shape assertion, the refusal a malformed arrival is answered with,
// and the subscription -- and not one of the five methods: VS3's Kotlin and
// VS4's Swift are what implement them, and VS3.1's module door is validated
// against `isVoiceSessionNative` from here rather than inventing a second list.
//
// **Why the port is handed in and never imported.** The module's own scan
// (`index.test.js`) forbids a file in this directory to import *any* package,
// which is what keeps a device, a database or an SDK out of the session. A
// native module arrives as a package name, so the session is given a port --
// `createVoiceSession({ native })`, and `mobile/index.js` is the one place that
// decides what answers it (VS1.3 wires the fake; a store build wires the
// module). That is also why the two tiny verdict helpers below are written here
// rather than imported from `src/audio/refusal.js`: production code in this
// directory reaches nothing outside it, which is F24's strongest form.
//
// **The validator is at delivery, and that is the point of the event half.**
// `events.js` says why it exists: the mistake a native module is most likely to
// make -- its own event name, its own route name, a `shouldResume` it forgot --
// should cost one refusal at the boundary rather than a wrong transition deep
// inside, and §5's six kinds are the only events anything downstream can be
// asked about. `deliver` is therefore the one door an event comes through: a
// well-formed one is handed to the subscribers as a frozen copy, and anything
// else is answered with a value. `deliver` is the platform's side of the port
// and `on` is the app's; the fake's `emit` is a test's.
//
// **D213 is why a refusal carries a code and a field, and nothing else.** §10's
// reporting is P3.5's shape -- a refusal code and the field it names -- and the
// fence is as important as the shape: no audio, no transcript, and no sentence
// the platform wrote, so the refusal has no `message` and repeats nothing that
// arrived (F9, F25). The field is named where this port can honestly name one:
// §5's kinds are told apart by `type`, so an arrival that is not one of the six
// is named against `type`, and a well-kinded event with a field outside its
// vocabulary is refused with `null` -- the validator answers yes or no and names
// no field, and a port that guessed one would be a second copy of
// `SESSION_EVENT_FIELDS`, drifting from the first. `SCREEN_CODES.BAD_PRESENCE`
// is the same `null`, for the same reason.
//
// **The subscription is one stream, and a mistake in it throws.** `on(handler)`
// and not `on(kind, handler)`: §5's event carries its own `type` and the
// session's `dispatch` takes the whole event, so a second dispatch table here
// would be one more place a kind could be misspelled. A handler that is not a
// function is a caller that can never work, so it is a `TypeError` and not a
// refusal -- `refusal.js`'s rule, and `isAudioIn`'s: a phone that says no is a
// value, a caller that is wrong is a throw.

import { SESSION_EVENT_FIELDS, isVoiceSessionEvent } from './events.js';

/**
 * The six methods of §5's `VoiceSessionNative`: the five the spec names, plus
 * the subscription the events of §5 arrive on.
 *
 * A frozen list rather than six `typeof` checks written out, because it is also
 * the answer to "is this the port": VS3.1's module door and this module's fake
 * are both held to it, so a seventh method is an edit here.
 */
export const NATIVE_METHODS = Object.freeze([
  'start',
  'updateStatus',
  'setMuted',
  'end',
  'cleanupStaleSessions',
  'on',
]);

/**
 * The refusals this port answers with, in the shape this tree's modules answer
 * with (`SCREEN_CODES`, `AUDIO_IN_CODES`).
 */
export const NATIVE_CODES = Object.freeze({
  /**
   * An arrival that is not one of §5's six well-formed kinds. The port refuses
   * it so that nothing downstream has to ask whether a platform's string is a
   * route, a reason or a state -- see `deliver`.
   */
  NOT_AN_EVENT: 'not-an-event',
});

/** A frozen refusal, in the shape this repository's modules use. */
function refuse(code, field = null) {
  return Object.freeze({ ok: false, code, field });
}

/** A frozen acceptance carrying the values the caller asked for. */
function accept(fields = {}) {
  return Object.freeze({ ok: true, ...fields });
}

/**
 * Whether `value` is §5's `VoiceSessionNative`. A shape check, deliberately
 * shallow in the way `isVoicePipeline` and `isAudioIn` are: it cannot promise
 * that a phone behaves, only that this is not the wrong object handed to the
 * right parameter -- the mistake that costs the most and is the cheapest to
 * catch, made at the point the session is built rather than at the phone call
 * the feature exists for.
 *
 * It says nothing about a port that carries *more* than the six: a fake has its
 * scripting surface and a native module has its own door, and neither is an
 * offence.
 *
 * @param {unknown} value a candidate port
 * @returns {boolean}
 */
export function isVoiceSessionNative(value) {
  if (value === null || typeof value !== 'object') return false;
  return NATIVE_METHODS.every((name) => typeof value[name] === 'function');
}

/**
 * The field a refused arrival is named against, where this port can name one.
 *
 * `type` is §5's discriminator, so an arrival that is not an object, or whose
 * `type` is not one of the six, is named against it -- and `Object.hasOwn` and
 * not `SESSION_EVENT_FIELDS[value.type]`, because a `type` naming an
 * `Object.prototype` member would otherwise resolve to an inherited value and
 * turn a refusal into a crash (the same trap `isVoiceSessionEvent` documents).
 * Everything else is `null`: a well-kinded event whose fields are wrong is
 * refused without a guess at which of them it was.
 *
 * @param {unknown} value the arrival that was refused
 * @returns {string | null}
 */
function fieldOf(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return null;
  return Object.hasOwn(SESSION_EVENT_FIELDS, value.type) ? null : 'type';
}

/**
 * The event half of §5's port: one subscription set, and the validator at
 * delivery.
 *
 * The two functions are frozen together so that a port is handed one object
 * whose halves cannot be separated -- a native module's JS door bridging its
 * emitter into `deliver` while the app subscribes through `on` is exactly the
 * shape this is for, and the fake (VS1.2, `fake.js`) is the same two functions
 * with a test on the `deliver` side.
 *
 * `deliver` answers the same value shape as the rest of this tree: the frozen
 * copy the subscribers were given, or the refusal. A subscriber that throws is
 * not caught here: swallowing it would be exactly the silent failure the mobile
 * rules forbid, and this port has no reporter to write it to, so it reaches the
 * caller above -- the platform's own event callback. The handlers are called in
 * subscription order, over a copy of the set, so an unsubscribe during a
 * delivery cannot change who hears *this* event.
 *
 * @returns {{ on: (handler: (event: object) => void) => () => boolean, deliver: (event: unknown) => object }}
 */
export function createNativeEvents() {
  const handlers = new Set();

  return Object.freeze({
    /**
     * Subscribe to the port's event stream. Returns the unsubscribe, which is
     * idempotent, like `FakeAudioIn.on`'s.
     *
     * @param {(event: object) => void} handler
     * @returns {() => boolean}
     * @throws {TypeError} when the handler is not a function
     */
    on(handler) {
      if (typeof handler !== 'function') throw new TypeError('a handler is a function');
      handlers.add(handler);
      return () => handlers.delete(handler);
    },

    /**
     * Hand one arrival to the port's subscribers -- the platform's side of the
     * stream.
     *
     * The event is copied and frozen before it is delivered, so a platform that
     * mutates what it sent, or a subscriber that would, changes nothing another
     * subscriber sees: what the session reduces is the event as it arrived.
     *
     * @param {unknown} event a candidate event, from the platform
     * @returns {{ ok: true, event: object } | { ok: false, code: string, field: string | null }}
     */
    deliver(event) {
      if (!isVoiceSessionEvent(event)) return refuse(NATIVE_CODES.NOT_AN_EVENT, fieldOf(event));
      const delivered = Object.freeze({ ...event });
      for (const handler of [...handlers]) handler(delivered);
      return accept({ event: delivered });
    },
  });
}
