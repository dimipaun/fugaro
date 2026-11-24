// The mobile session port's stub (mobile identity and pairing plan, Task C2).
//
// A paired project's session that does nothing: `stubSession()` returns a
// **fixed fake** -- the same project id, endpoint, role, policy values and
// token on every call, and a 64-byte signature that signs nothing and does not
// even look at the bytes. Surfaces are written against the port (J2, J3, J4, X1,
// Z1, J5b) and should not each invent a fake, so the plan puts one here; **I2
// deletes this file** once M3's real session is what the surfaces mount, and
// nothing but a test may import it.
//
// **What it is not.** It touches no key, no store, no clock and no network, and
// it proves nothing about M3: a test that passes against this stub says the
// surface reads the port's fields, not that a refresh, a signature or a role is
// real. `assertSessionPort` is what keeps the stub and M3 the same shape, and
// this module runs its own fake through it, so a stub that drifts from the port
// fails at once rather than teaching three surfaces a shape M3 will not have.
//
// **Why the driver is here.** A surface subscribes to `signedOut` and stops,
// and a test has to be able to make that happen; the listener belongs to the
// surface, so the test cannot reach it. `stubSignOut(session)` is that seam: it
// fires the event once, exactly as M3 fires it when a rotated-out refresh token
// makes the session unrecoverable, and a session it did not build is refused
// rather than driven. Nothing here is part of the port -- a surface never calls
// it, and I2 removes it with the file.

import { PORT_CODES, SESSION_EVENTS, SESSION_ROLES, SessionPortError, assertSessionPort } from './port.js';

/** The stub project's opaque id: not a name, not a real id. */
export const STUB_PROJECT_ID = 'stub-project';

/** The stub project's endpoint: `.invalid` is reserved and never resolves. */
export const STUB_ENDPOINT = 'https://stub-project.invalid';

/** The fixed access token `idToken()` resolves to. A placeholder, not a JWT. */
export const STUB_ACCESS_TOKEN = 'stub-access-token';

/** The modes the stub project names as costly, which are P6's default pair. */
export const STUB_COSTLY_MODES = Object.freeze(['pro', 'critical']);

/** The stub project's auto-compaction threshold: a fraction of the window. */
export const STUB_COMPACTION_THRESHOLD = 0.4;

/** Whether the stub project's app may send crash reports: P6's default, on. */
export const STUB_CRASH_REPORTS = true;

// Ed25519's signature length, which is what the device key's signature is. The
// stub's vector is a repeated byte: signature-SHAPED and nothing more, so a
// caller that checked a value against it would be checking a constant.
const SIGNATURE_LENGTH = 64;
const STUB_SIGNATURE_BYTE = 0x5a;

// What a stub session knows that the port does not carry: who is listening, and
// whether the session is over. Keyed by the session object, so two stub
// sessions never share a listener.
const STUB_STATE = new WeakMap();

function refuse(code, member) {
  throw new SessionPortError(code, member);
}

function stateOf(session) {
  const state = STUB_STATE.get(session);
  if (state === undefined) {
    throw new TypeError('stubSignOut: this session was not built by stubSession');
  }
  return state;
}

/**
 * A fake session that conforms to the port. `overrides` replaces named members,
 * which is how a test asks for a viewer's session or a project with no costly
 * mode; `overrides.policy` replaces the fields it names and keeps the rest. The
 * result is run through `assertSessionPort` and frozen, so an override that
 * breaks the shape is refused here rather than read by a surface later, and a
 * test cannot make a surface pass by mutating the session it handed it.
 *
 * Every call builds its own session and its own listeners; `stubSignOut` on one
 * leaves the others alone.
 */
export function stubSession(overrides = {}) {
  // A policy override replaces the fields it names and keeps the rest: a test
  // that asks for a project with no costly mode still gets the stub's switch and
  // threshold, which is what a session that only half-carried a policy would not.
  const { policy: policyOverride = {}, ...rest } = overrides;
  const policy = {
    costly_modes: [...STUB_COSTLY_MODES],
    biometric_on_costly: false,
    compaction_threshold: STUB_COMPACTION_THRESHOLD,
    crash_reports: STUB_CRASH_REPORTS,
    ...policyOverride,
  };

  const state = { signedOut: false, listeners: new Set() };

  const session = assertSessionPort({
    project_id: STUB_PROJECT_ID,
    endpoint: STUB_ENDPOINT,
    role: SESSION_ROLES.OPERATOR,
    async idToken() {
      if (state.signedOut) refuse(PORT_CODES.SIGNED_OUT, 'idToken');
      return STUB_ACCESS_TOKEN;
    },
    async signMessage(bytes) {
      if (state.signedOut) refuse(PORT_CODES.SIGNED_OUT, 'signMessage');
      if (!(bytes instanceof Uint8Array)) refuse(PORT_CODES.BAD_TYPE, 'bytes');
      return new Uint8Array(SIGNATURE_LENGTH).fill(STUB_SIGNATURE_BYTE);
    },
    on(event, listener) {
      if (event !== SESSION_EVENTS.SIGNED_OUT) refuse(PORT_CODES.UNKNOWN_EVENT, 'event');
      if (typeof listener !== 'function') refuse(PORT_CODES.BAD_LISTENER, 'listener');
      // The event has already happened: a surface subscribing after it missed
      // it, and answering "nothing" would leave the surface believing the
      // session is live. The refusal is the answer.
      if (state.signedOut) refuse(PORT_CODES.SIGNED_OUT, 'event');
      state.listeners.add(listener);
      return () => state.listeners.delete(listener);
    },
    ...rest,
    policy,
  });

  STUB_STATE.set(session, state);
  return Object.freeze(session);
}

/**
 * End a session this stub built: fire `signedOut` on every listener once, in
 * the order they subscribed, and clear them. A second call does nothing -- a
 * signed-out session stays signed out -- and until I2 removes this file, a test
 * that wants the surfaces to stop calls this.
 */
export function stubSignOut(session) {
  const state = stateOf(session);
  if (state.signedOut) return;
  state.signedOut = true;
  const listeners = [...state.listeners];
  state.listeners.clear();
  for (const listener of listeners) listener();
}
