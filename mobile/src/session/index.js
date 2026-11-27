// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The session module's door: C2's port over one project (task M3; D2783,
// D2794, C2).
//
// **What this module is.** `createProjectSession` builds the value C2's
// `assertSessionPort` describes -- the project's id and endpoint, the roster's
// role, the policy, an `idToken()` that refreshes, a `signMessage(bytes)` that
// signs with the device key, and one event -- and `signOutEverywhere` is
// D2794's other half: revoke every live project's family and sign the
// identities out.
//
// **What a screen can and cannot reach.** The door exports the factory, the
// sign-out, the refusal vocabulary and the algorithm's name, and nothing else:
// no `fetch`, no request builder, no store and no key. A screen cannot hand a
// token to a URL of its own, because the only thing it holds is a session whose
// two accessors are the port's. The network, the store and the primitive are
// the entry's (`mobile/index.js`), passed in here -- which is why every test of
// this module runs with no device, no Keychain and no `node_modules`.
//
// **The refresh is the session's, and the token stays in it.** `idToken()`
// refreshes when the held token is near its hour (the port's prose contract,
// pinned by `docs/mobile-identity-pairing/session-port.md`): it signs the held
// token with the device key, presents it at the project's refresh route, stores
// the successor and exchanges the custom token for an ID token. The token is
// never a member of the session, so no surface can hold one.
//
// **A refusal that ends the session ends it first.** A refresh the project
// answers `device_revoked` -- the token another device rotated out, an admin
// revoke, a membership that went away -- is not a failure the phone retries:
// the token is dead, so the session signs the project out (delete the stored
// token, fire `signedOut` once) and *then* rejects. That order is the whole
// point of the red step: a caller that catches the rejection finds a session
// already refusing rather than one that still looks live.
//
// **A role here is a display claim, never an authorisation** (F14, D2791), and
// this module is where that is easy to get wrong: a refresh rewrites the role
// from the roster, so the session's `role` and `policy` are read through
// getters -- a surface that mounted the value keeps seeing the current role
// rather than the one it paired with, and nothing here treats either as a
// grant. What a role may do is decided at the consumer.

import { PORT_CODES, SESSION_EVENTS, SessionPortError, assertSessionPort } from '../../../shared/mobile-session/port.js';

import { DEVICE_KEY_ALGORITHM } from './devicekey.js';
import { REFRESH_CODES, REFRESH_SKEW_MS, readSessionPolicy, refreshSession, revokeSelf } from './refresh.js';

export { DEVICE_KEY_ALGORITHM };

/**
 * The refusals a caller of this door switches on: this module's own
 * (`refresh.js`'s) and the port's, which are read from C2 rather than
 * respelled. The two sets do not overlap, so one name is one code.
 */
export const SESSION_CODES = Object.freeze({ ...REFRESH_CODES, ...PORT_CODES });

function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/** The three methods the session needs of a project session store. */
function isStore(store) {
  return (
    store !== null &&
    typeof store === 'object' &&
    typeof store.get === 'function' &&
    typeof store.put === 'function' &&
    typeof store.delete === 'function'
  );
}

/** The one method the session needs of a device key store. */
function isDeviceKey(deviceKey) {
  return deviceKey !== null && typeof deviceKey === 'object' && typeof deviceKey.sign === 'function';
}

/** One project's session, as C2's port describes it. */
class ProjectSession {
  #projectId;
  #endpoint;
  #apiKey;
  #store;
  #deviceKey;
  #fetch;
  #now;
  #role;
  #policy;
  #refreshToken;
  #idToken;
  #expiresAt;
  #pending = null;
  #signedOut = false;
  #listeners = new Set();

  constructor(options) {
    const { projectId, endpoint, apiKey, store, deviceKey, fetch, now, role, policy, refreshToken } = options;
    this.#projectId = projectId;
    this.#endpoint = endpoint;
    this.#apiKey = apiKey;
    this.#store = store;
    this.#deviceKey = deviceKey;
    this.#fetch = fetch;
    this.#now = now;
    this.#role = role;
    this.#policy = policy;
    this.#refreshToken = refreshToken;
    // A session is built before its first refresh: the pairing exchange that
    // yielded the custom token is the caller's (M2), so the ID token it already
    // holds is handed in when there is one, and the first `idToken()` refreshes
    // when there is not.
    this.#idToken = typeof options.idToken === 'string' && options.idToken !== '' ? options.idToken : null;
    this.#expiresAt = Number.isFinite(options.expiresIn) && options.expiresIn > 0 ? this.#now() + options.expiresIn * 1000 : 0;
  }

  /** The members C2 names, and nothing else: the port is the whole surface. */
  members() {
    const self = this;
    return {
      project_id: self.#projectId,
      endpoint: self.#endpoint,
      idToken: () => self.idToken(),
      signMessage: (bytes) => self.signMessage(bytes),
      on: (event, listener) => self.on(event, listener),
      // The role and the policy are read live, because a refresh rewrites both
      // (O14, D2791): a surface that mounted this value keeps seeing the roster
      // rather than the pair-time answer.
      get role() {
        return self.role;
      },
      get policy() {
        return self.policy;
      },
    };
  }

  #refuse(code, member) {
    throw new SessionPortError(code, member);
  }

  #assertLive(member) {
    if (this.#signedOut) this.#refuse(PORT_CODES.SIGNED_OUT, member);
  }

  /**
   * The access token, refreshed through the project when the held one is near
   * its hour.
   *
   * **One rotation at a time, and that is a correctness property and not an
   * optimisation.** The port hands six surfaces one session value, so the app
   * returning to the foreground after an hour makes them all call this on the
   * same tick -- and the token rotates on every use (D2783), so a second
   * rotation presenting the token the first has already burned is answered
   * `device_revoked`: the service revokes the whole family and deletes the
   * device node, and the phone's own concurrency has cost it a QR re-pair. So
   * concurrent callers are handed the one pending rotation, and a caller that
   * arrives while it is in flight waits for its answer rather than starting a
   * second one.
   *
   * A refusal that ends the session signs out first (see the header) and is
   * then thrown, so the caller's `catch` sees a session that is already over
   * rather than one it has to remember to end -- and a caller that waited on a
   * rotation which ended the session is refused the same way, which is what the
   * re-check after it settles is for.
   */
  async idToken() {
    this.#assertLive('idToken');
    if (this.#idToken !== null && this.#now() < this.#expiresAt - REFRESH_SKEW_MS) return this.#idToken;
    if (this.#pending === null) this.#pending = this.#rotate();
    const pending = this.#pending;
    try {
      await pending;
    } finally {
      // Released whichever way it went: a refresh that failed for a reason the
      // next call may not meet (the network) must be retryable.
      if (this.#pending === pending) this.#pending = null;
    }
    // The rotation the caller waited on may have ended the session (it is the
    // one that fires `signedOut`), and a token from it would be stale.
    this.#assertLive('idToken');
    return this.#idToken;
  }

  /** One rotation: sign, present, store the successor, exchange the custom token. */
  async #rotate() {
    const refreshed = await refreshSession({
      endpoint: this.#endpoint,
      refreshToken: this.#refreshToken,
      // `refresh.js` takes a function that returns bytes, and the device key
      // store answers a refusal envelope: the adapter is where the two meet,
      // and a key that is gone (or unreadable) is thrown as its own code so the
      // caller learns which it was rather than only that signing failed.
      sign: async (message) => {
        const signed = await this.#deviceKey.sign(this.#projectId, message);
        if (!signed.ok) this.#refuse(signed.code, 'idToken');
        return signed.signature;
      },
      apiKey: this.#apiKey,
      fetch: this.#fetch,
    });
    if (!refreshed.ok) {
      if (refreshed.revoked === true) {
        await this.#signOut();
        this.#refuse(refreshed.code, 'idToken');
      }
      // A rotation whose exchange failed still rotated (see `refreshSession`),
      // so the successor it carries is the live token and the one in hand is
      // dead: it is written before the refusal, because a phone that kept the
      // dead one would present it next time and be told `device_revoked`. The
      // write's own failure is the refusal then -- a rotated token that is not
      // written is the same broken phone -- and it is reported rather than
      // swallowed.
      if (typeof refreshed.refreshToken === 'string' && refreshed.refreshToken !== '') {
        this.#refreshToken = refreshed.refreshToken;
        const stored = await this.#store.put(this.#projectId, refreshed.refreshToken);
        if (!stored.ok) this.#refuse(stored.code, 'idToken');
      }
      this.#refuse(refreshed.code, 'idToken');
    }
    // The successor is stored before it is cached: a token the phone has
    // rotated but not written is a phone that cannot refresh again, and the
    // caller must see that as a failure rather than as a working session.
    const stored = await this.#store.put(this.#projectId, refreshed.refreshToken);
    if (!stored.ok) this.#refuse(stored.code, 'idToken');
    this.#refreshToken = refreshed.refreshToken;
    this.#idToken = refreshed.idToken;
    this.#expiresAt = this.#now() + refreshed.expiresIn * 1000;
    // The roster's role and the project's policy are rewritten at every
    // refresh (O14, D2791): a person whose membership changed learns it here,
    // and a surface reads them through the getters below.
    this.#role = refreshed.role;
    this.#policy = refreshed.policy;
    return this.#idToken;
  }

  /** Sign bytes with the device key. The key never leaves this module. */
  async signMessage(bytes) {
    this.#assertLive('signMessage');
    if (!(bytes instanceof Uint8Array)) this.#refuse(PORT_CODES.BAD_TYPE, 'bytes');
    const signed = await this.#deviceKey.sign(this.#projectId, bytes);
    if (!signed.ok) this.#refuse(signed.code, 'signMessage');
    return signed.signature;
  }

  /** The one event a surface may rely on. */
  on(event, listener) {
    if (event !== SESSION_EVENTS.SIGNED_OUT) this.#refuse(PORT_CODES.UNKNOWN_EVENT, 'event');
    if (typeof listener !== 'function') this.#refuse(PORT_CODES.BAD_LISTENER, 'listener');
    // The event has already happened: a surface subscribing after it missed it,
    // and answering "nothing" would leave it believing the session is live.
    this.#assertLive('event');
    this.#listeners.add(listener);
    return () => this.#listeners.delete(listener);
  }

  /**
   * End the session: forget the stored token, then tell every listener once.
   *
   * **The token is deleted first and the event fires after**, so a listener
   * that reads the store when it is told finds nothing rather than the
   * credential it is being told about. A delete that fails is deliberately not
   * a reason to keep the session alive -- the session is over either way, and
   * the token's item is reached by no code path once the project is gone.
   */
  async #signOut() {
    if (this.#signedOut) return;
    this.#signedOut = true;
    this.#idToken = null;
    this.#expiresAt = 0;
    await this.#store.delete(this.#projectId);
    const listeners = [...this.#listeners];
    this.#listeners.clear();
    for (const listener of listeners) listener();
  }

  /** The roster's role as of the last refresh. A display claim (D2791). */
  get role() {
    return this.#role;
  }

  /** The project's policy as of the last refresh. */
  get policy() {
    return this.#policy;
  }
}

/**
 * One paired project's session, as C2's port describes it.
 *
 * Returns `{ ok: true, session }` or a refusal. The value is run through
 * `assertSessionPort` before it is handed back -- the port is the contract and
 * this module does not restate its rules -- so a caller's bad role or endpoint
 * is a refusal here, at construction, rather than a surface mounting a value
 * the port would refuse.
 *
 * `store`, `deviceKey`, `fetch` and `now` are the app's ports and are the
 * caller's (`mobile/index.js`); a missing one is a `TypeError`, because that is
 * a wiring defect and not data. `role`, `policy`, `endpoint`, `projectId` and
 * `refreshToken` come from the pairing answer and are validated as the port's
 * own shape, so a refusal names the member.
 *
 * @param {object} options
 * @param {string} options.projectId the project's opaque id
 * @param {string} options.endpoint the project's mobile endpoint
 * @param {string} options.role the roster's role for this person
 * @param {object} options.policy C1's `SessionPolicy` shape
 * @param {string} options.refreshToken the stored Fugaro refresh token
 * @param {object} options.store a `ProjectSessionStore`
 * @param {object} options.deviceKey a `DeviceKeyStore`
 * @param {string} options.apiKey the project's restricted Firebase API key
 * @param {Function} options.fetch the platform's `fetch`
 * @param {Function} [options.now] the clock, for the hour's arithmetic
 * @param {string} [options.idToken] the token the pairing exchange already yielded
 * @param {number} [options.expiresIn] that token's life, in seconds
 */
export function createProjectSession(options = {}) {
  const { store, deviceKey, fetch, now = Date.now } = options;
  if (!isStore(store)) throw new TypeError('a project session store is required');
  if (!isDeviceKey(deviceKey)) throw new TypeError('a device key store is required');
  if (typeof fetch !== 'function') throw new TypeError('a fetch port is required');
  if (typeof now !== 'function') throw new TypeError('a clock is required');

  // The policy arrives in C1's shape (a percent) and the port carries a
  // fraction, so the unit changes here; the shape is read before it is mapped,
  // because a mapper is fail-open and a policy the port would accept is one a
  // screen renders (see `readSessionPolicy`).
  const read = readSessionPolicy(options.policy);
  if (!read.ok) return refuse(read.code, read.field);

  const built = new ProjectSession({ ...options, store, deviceKey, fetch, now, policy: read.policy });
  const members = built.members();
  try {
    assertSessionPort(members);
  } catch (error) {
    if (error instanceof SessionPortError) return refuse(error.code, error.member);
    throw error;
  }
  return Object.freeze({ ok: true, session: Object.freeze(members) });
}

/**
 * Sign out everywhere (D2794): revoke each live project's own family, then sign
 * the identities out.
 *
 * **The projects are the caller's.** Which projects are live is the paired
 * list's (K3), and this module holds no such list: what it is handed is one
 * `{ endpoint, refreshToken, sign }` per live project, and what it returns is
 * which of them were revoked and which were not, so a caller can keep the ones
 * that failed rather than pretend the sign-out was complete. A project that
 * refuses the revoke is not an error the phone retries into a loop: the family
 * it could not end is named, and the identity sign-out runs regardless, because
 * a person who asked to sign out is signed out of the phone whatever a project
 * answered.
 *
 * `identities` is the identity half (M1's store) and is called last: it is a
 * function rather than a list for the same reason, so this module needs no
 * identity vocabulary and no network of its own.
 */
export async function signOutEverywhere({ projects = [], identities, fetch } = {}) {
  const revoked = [];
  const failed = [];
  for (const project of projects) {
    const done = await revokeSelf({
      endpoint: project.endpoint,
      refreshToken: project.refreshToken,
      sign: project.sign,
      fetch,
    });
    if (done.ok) revoked.push(project.endpoint);
    else failed.push(Object.freeze({ endpoint: project.endpoint, code: done.code }));
  }
  let signedOutIdentities = true;
  if (typeof identities === 'function') {
    try {
      await identities();
    } catch {
      // A failure here is the identity store's, and it is reported rather than
      // thrown: the projects above are already revoked, and a caller that got
      // an exception would not learn which ones.
      signedOutIdentities = false;
    }
  }
  return Object.freeze({
    ok: failed.length === 0 && signedOutIdentities,
    revoked: Object.freeze(revoked),
    failed: Object.freeze(failed),
  });
}
