// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The push token, as the app asks for it -- and the one thing this module
// refuses to do with one: guess (mobile plan, Task P5.2.8; the design is
// docs/mobile/design/push-notifications.md §3.5 (b) and question 10).
//
// **Why the token arrives through a port rather than an import.** The SDK that
// mints a token is a bare package, and a module that imported one at the top
// could not be loaded by this app's tests: `mobile/README.md` is explicit that
// the suite runs in a fresh clone with no `node_modules`, so the SDK is reached
// in `mobile/index.js` -- the entry, the one file `write-path.test.js`'s rule 5
// allows `@react-native-firebase/*` in -- and what reaches this directory is
// the port (`relay.js` is that port's real shape). The rule is the one
// `createMailboxClient` follows for the database: the entry knows what a device
// is, a screen never does.
//
// **What a source answers, exactly.** `token()` is awaited and may do three
// things: answer `{ok: true, token}`; answer a refusal `{ok: false, code,
// field}` when there is no token to be had and the reason is known
// (`relay.js`'s `relay-failed`, or this module's `no-token`); or THROW, when
// the port itself failed. A source that answered `''` or `null` for "no token"
// is understood too and reported as `no-token` rather than written as an empty
// field -- but a throw is the shape a broken device reports itself in, and
// reading it as "no token" would send the owner to look for a token in a
// settings screen when the app's Firebase wiring is what is broken. The three
// answers become three codes: `token-failed`, the source's own, and the
// schema's after `judgeField` has had the string.
//
// **A token is validated against the schema's bound and never logged.** The
// length and character rules are `shared/mailbox-schema`'s (`TOKEN_MAX`,
// `bad-character`), asked through `judgeField`, so a token this app accepts is
// one the consumer would accept; and no code path here puts a token in a
// message, a refusal's `field` (the schema's answer names `token`, never the
// value) or a screen.

import { judgeField } from './message.js';

/** The refusal codes this module adds to the schema's and the source's. */
export const TOKEN_CODES = Object.freeze({
  /** The source answered, and there is no token: the phone is not registered. */
  NO_TOKEN: 'no-token',
  /** The source threw, or answered something that is not a token answer. */
  TOKEN_FAILED: 'token-failed',
});

function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/** A plain object, and not null, an array, or an instance of something. */
function isPlainObject(value) {
  return value !== null && typeof value === 'object' && Object.getPrototypeOf(value) === Object.prototype;
}

/**
 * Ask a token source for the phone's token, and answer a token or a refusal.
 *
 * @param {object} source the port: `token()`, awaited
 * @returns {Promise<{ok: true, token: string}|{ok: false, code: string, field: string}>}
 * @throws {TypeError} for a source that is not one, which is a wiring bug and
 *   not a device failure -- the app builds this once, where it has an account,
 *   and a source that could exist without a `token()` would fail later, at the
 *   one moment the owner is waiting for the app to come up.
 */
export async function readPushToken(source) {
  if (source === null || typeof source !== 'object' || typeof source.token !== 'function') {
    throw new TypeError('readPushToken: a token source with token() is required');
  }
  let answer;
  try {
    answer = await source.token();
  } catch {
    // Deliberately not carrying the error through: an SDK's own message can
    // name the app instance, the project or a file path, and this is the one
    // place a token's neighbourhood is read. The code is the report.
    return refuse(TOKEN_CODES.TOKEN_FAILED, 'token');
  }
  if (answer === null || answer === undefined || answer === '') {
    return refuse(TOKEN_CODES.NO_TOKEN, 'token');
  }
  if (!isPlainObject(answer)) return refuse(TOKEN_CODES.TOKEN_FAILED, 'token');
  if (!answer.ok) {
    return refuse(typeof answer.code === 'string' ? answer.code : TOKEN_CODES.NO_TOKEN, 'token');
  }
  if (typeof answer.token !== 'string' || answer.token === '') {
    return refuse(TOKEN_CODES.NO_TOKEN, 'token');
  }
  const refused = judgeField('token', answer.token);
  if (refused !== null) return refuse(refused.code, refused.field);
  return Object.freeze({ ok: true, token: answer.token });
}

/**
 * A token source a test (and a wiring without a device) can drive: one token,
 * settable, or a source that fails.
 *
 * `set` is what makes the rotation case testable -- a re-registration with a
 * new token is §3.5's ordinary case -- and `fail` is the device that throws,
 * which is a different answer from `set(null)`, and the two are tested apart.
 */
export class MemoryTokenSource {
  #token;
  #fail;

  /**
   * @param {object} [options]
   * @param {string|null} [options.token] the token to answer, or `null` for none
   * @param {boolean} [options.fail] throw instead of answering
   */
  constructor({ token = 'synthetic-fcm-token-0123456789', fail = false } = {}) {
    this.#token = token;
    this.#fail = fail === true;
  }

  /** Change what the next call answers -- a rotation, or a phone with no token. */
  set(token) {
    this.#token = token ?? null;
  }

  async token() {
    if (this.#fail) throw new Error('the token source failed');
    return this.#token === null ? { ok: false, code: TOKEN_CODES.NO_TOKEN, field: 'token' } : { ok: true, token: this.#token };
  }
}
