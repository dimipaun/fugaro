// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The registration: the token, the switches, and the one message that carries
// both (mobile plan, Task P5.2.8; D113, D169; the design is
// docs/mobile/design/push-notifications.md §3.5 (b), §3.8 and §6).
//
// **The whole path, and where it goes.** A registration is an ordinary mailbox
// message (P5.2.1's `registration` kind) written by the app's **only** write
// path -- `src/mailbox/client.js`'s `register`, which signs it with D171's
// per-pairing key over P6.3's canonical bytes -- and applied by whoever holds
// `roles/firebasedatabase.admin` to `config/admin/push/$uid` (P5.2.4). Nothing
// in this directory writes the database, and nothing in it can: the app's
// `write-path.test.js` still finds one writer, which is what §3.5 (b) chose
// over letting the phone write its own push node.
//
// **Every call writes, and that is the point rather than an oversight.** A
// registration is not suppressed when "nothing changed", and the reason is
// §6's second row: when the sender finds a token stale (FCM's `UNREGISTERED`)
// it DELETES it and waits for the app to register again -- so an app that
// skipped the write because its token and switches looked unchanged would never
// come back, and a phone whose token was reaped would stay silent for ever with
// nothing to notice. The message is idempotent at the other end (the same
// token, the same switches, and the consumer's ledger answers a duplicate), so
// re-sending it is the cheap side of that trade. What the app does *not* do is
// report a success it did not have: a token it could not obtain skips the write
// entirely (`no-token`), and the state below stays `unregistered` so the next
// open asks again.
//
// **What asks first, and in what order.** The switches are read from the
// settings record (a store that cannot be read is a refusal rather than a
// message without them -- §3.8's controls ride in THIS message, so a
// registration that could not say what the switches are is not one), then the
// token is asked for, and only then is the client called. The client asks the
// lapse and the connection gate itself, before it mints anything (see its
// header), so a lapsed or offline phone costs a read and nothing else.
//
// **The retry is the next call, and there is no timer here.** §3.5 (b) registers
// the phone "on next open", and §6's recovery is the same act; the app has no
// backoff, no queue and no stored state to go stale -- what it has is the state
// below, which a screen renders, and the fact that `register()` is safe to call
// on every open, after a token refresh, and after a switch is flipped.
// (Q10's other lever -- FCM's `deleteToken` when an installation is left --
// belongs to whichever task wires the app's revoke; no such call is built.)
//
// **The calls are SERIALIZED, and the reason is the switches' read.** A
// registration reads the record, then awaits the token -- the relay's network
// call, the slow half -- and only then writes, with the client stamping the
// message `at`. Two overlapping calls would therefore each read a record the
// other had already replaced, and the consumer applies inbox items **in `at`
// order** (`internal/mailbox/inbox.go` sorts on the record's own `at`) and
// replaces `prefs` whole (`internal/cli/mailbox.go`'s `runRegistration`), so the
// OLDER settings could be the ones that landed with the LATER `at` -- the
// owner's newest flip silently not honoured. `controls.js` serializes the store
// halves and says why; that queue cannot cover this one, whose work is a token
// call and a mailbox write, so the queue is here, per client (one per account,
// `push.js`). What it buys is the order the read depends on: each call reads the
// switches only after the previous call's write is done, so the last write
// carries the last state of the record.

import { judgeField } from './message.js';
import { readPushToken } from './token.js';

/** What the app last managed, as the screen renders it. */
export const PUSH_STATES = Object.freeze({
  /** `register()` has not been called on this phone. */
  NEVER: 'never',
  /** The last call wrote a registration the mailbox took. */
  REGISTERED: 'registered',
  /** The last call wrote nothing: a token, a switch, a gate or the store. */
  UNREGISTERED: 'unregistered',
});

function refuse(state, code, field) {
  return Object.freeze({ ok: false, state, code, field, id: null, at: null });
}

/**
 * The tail of each account's queue, keyed by the mailbox client -- the writer
 * whose order the consumer's `at` ordering rests on (the header). The client is
 * the WeakMap key for the same reason `controls.js` uses the store: a door
 * dropped with its account takes its queue with it.
 */
const QUEUES = new WeakMap();

function serialize(client, work) {
  let previous = QUEUES.get(client) ?? Promise.resolve();
  const run = previous.then(work, work);
  QUEUES.set(
    client,
    run.then(
      () => undefined,
      () => undefined,
    ),
  );
  return run;
}

/** "Nothing has been asked yet", as the same shape everything else answers in. */
function never() {
  return Object.freeze({ ok: false, state: PUSH_STATES.NEVER, code: null, field: null, id: null, at: null });
}

/**
 * The registration half of P5.2.8: one call that turns "this phone has a token
 * and some switches" into one signed mailbox message.
 *
 * @param {object} options
 * @param {object} options.client the mailbox client (`createMailboxClient`) --
 *   the app's one write path, and the only thing here that signs or writes
 * @param {object} options.controls `createPushControls`'s door, read for the
 *   switches this message carries
 * @param {object} options.token a token source (`token.js`'s port)
 * @param {string} options.platform `ios` or `android`, as the device reports
 *   it. Judged once, here, because it cannot change while the app runs: a build
 *   whose platform the schema does not name registers nothing and says so
 *   (`unknown-platform`) rather than sending a platform the consumer refuses
 * @returns {object} the frozen door
 * @throws {TypeError} for a client, a controls door or a source that cannot be
 *   used -- a wiring bug, answered here rather than at the first registration
 */
export function createPushRegistration({ client, controls, token, platform } = {}) {
  if (client === null || typeof client !== 'object' || typeof client.register !== 'function') {
    throw new TypeError('createPushRegistration: a mailbox client with register() is required');
  }
  if (controls === null || typeof controls !== 'object' || typeof controls.read !== 'function') {
    throw new TypeError('createPushRegistration: a controls door with read() is required');
  }
  if (token === null || typeof token !== 'object' || typeof token.token !== 'function') {
    throw new TypeError('createPushRegistration: a token source is required');
  }
  const refusedPlatform = judgeField('platform', platform);

  let last = never();

  /**
   * Register this phone: read the switches, ask for the token, and write one
   * signed registration message through the mailbox client. The body of the
   * door's `register`, which runs it behind the client's own queue (the header):
   * one call at a time, so the read above cannot land on a record the previous
   * write already replaced.
   *
   * @returns {Promise<{ok: boolean, state: string, code: string|null,
   *   field: string|null, id: string|null, at: number|null}>}
   */
  async function registerNow() {
    if (refusedPlatform !== null) {
      last = refuse(PUSH_STATES.UNREGISTERED, refusedPlatform.code, refusedPlatform.field);
      return last;
    }
    const switches = await controls.read();
    if (!switches.ok) {
      // A store that cannot be read is not "no switches": the switches the
      // owner set are what the sender obeys, and a message that left them out
      // would leave the node holding whatever they were before. Nothing is
      // written, and the state says which record was at fault.
      last = refuse(PUSH_STATES.UNREGISTERED, switches.code, switches.field);
      return last;
    }
    const asked = await readPushToken(token);
    if (!asked.ok) {
      last = refuse(PUSH_STATES.UNREGISTERED, asked.code, asked.field);
      return last;
    }
    const written = await client.register({
      token: asked.token,
      platform,
      prefs: switches.prefs,
    });
    if (!written.ok) {
      last = refuse(PUSH_STATES.UNREGISTERED, written.code, written.field);
      return last;
    }
    last = Object.freeze({
      ok: true,
      state: PUSH_STATES.REGISTERED,
      code: null,
      field: null,
      id: written.id,
      at: written.at,
    });
    return last;
  }

  return Object.freeze({
    ok: true,
    register: () => serialize(client, registerNow),
    state: () => last,
  });
}
