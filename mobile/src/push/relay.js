// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The publisher's relay, and the token the mailbox carries: the phone's own
// device token goes to the relay, and what comes back is what is registered
// (mobile plan, Task P5.2.8; the owner's Q7 ruling of 2026-11-21; the design is
// docs/mobile/design/push-notifications.md §3.3, §3.5 (b) and §9).
//
// **Q7's answer, which replaced this module's first shape.** The owner ruled
// that the app registers with **a relay the publisher holds**, and that the
// publisher holds the delivery credentials -- the APNs `.p8` once, and the
// project's FCM reach. So an installation registers with the publisher's
// service rather than with its own Firebase project's sender, and the token it
// registers is the relay's: §3.3's own words are that "installations hold an
// Expo (or publisher) token **instead**" of the device address a direct sender
// would need. This file is that sentence in code -- the device token from
// `messaging.js` goes in, the relay's token comes out, and the token that
// reaches the mailbox is the relay's.
//
// **What this file deliberately does NOT define is the relay's wire protocol.**
// The relay is the publisher's service; it is not in this repository, and its
// endpoint, its request shape and its transport are the publisher's to define
// (`D124`'s host allow-list ships empty and the host is an owner decision
// first). So the relay arrives as a **port**, exactly as the database
// (`mailbox/store.js`), the connection (`mailbox/connection.js`) and the SDK
// do: `relay.register({ endpoint, deviceToken })` is the app's own side of the
// call, and its adapter -- the transport, and whatever else the relay's
// protocol needs of the app -- is the app's wiring, in `mobile/index.js`, where
// there is no decision to test. What is pinned HERE is the app's half:
//
//   * the relay is **named** (`endpoint`, required, never defaulted: a relay
//     reached by a built-in constant would be a second answer to "which
//     service", and D124's list is not this module's to choose);
//   * the phone's **device token** is what the relay is given;
//   * the relay's answer -- and no device token, even one the relay hands back
//     -- is what this source answers with.
//
// **Nothing here holds a secret, an account or a host.** The module passes the
// endpoint and the device token and nothing else: what the relay's protocol
// needs beyond them (the account it registers against, the app's own identity)
// belongs to the adapter, which the entry builds where the app's credentials
// already are. This directory's rule is that nothing here reads a store or a
// key, and it is a rule about what a registration *is*: §3.5 (b)'s message is
// signed by the mailbox client, the app's one write path (D113), and the relay
// call is not a write to the installation at all.

import { TOKEN_CODES } from './token.js';

/** The refusal codes this module answers with, beside `token.js`'s. */
export const RELAY_CODES = Object.freeze({
  /**
   * The relay did not answer a registration token of its own: it refused, it
   * threw, it answered nothing, or it handed the phone's device token back.
   * The last case is one code rather than its own, because it is the same fact
   * to the owner -- nothing has been registered -- and because the fix is the
   * relay's adapter either way (the test that pins it is named for it).
   */
  RELAY_FAILED: 'relay-failed',
});

function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/** A plain object, and not null, an array, or an instance of something. */
function isPlainObject(value) {
  return value !== null && typeof value === 'object' && Object.getPrototypeOf(value) === Object.prototype;
}

/** Whether an answer is a token answer this module can use. */
function tokenIn(answer) {
  return isPlainObject(answer) && answer.ok === true
    && typeof answer.token === 'string' && answer.token !== '';
}

/**
 * A token source that registers the phone with the publisher's relay and
 * answers the token the relay minted (see the header).
 *
 * @param {object} options
 * @param {object} options.device the phone's device-token source: `token()`,
 *   awaited, in `token.js`'s shape (`deviceTokenSource` is the real one, over
 *   the pinned SDK)
 * @param {object} options.relay the publisher's relay, as a port:
 *   `register({ endpoint, deviceToken })`, awaited, answering `{ok: true,
 *   token}` or a refusal
 * @param {string} options.endpoint the publisher's relay endpoint. Required:
 *   the relay is named rather than defaulted, and the adapter is given the name
 *   it must use
 * @returns {{token: () => Promise<object>}} the source `readPushToken` takes
 * @throws {TypeError} for a half port or an unnamed endpoint -- wiring bugs,
 *   refused at construction rather than at the first registration
 */
export function relayTokenSource({ device, relay, endpoint } = {}) {
  if (device === null || typeof device !== 'object' || typeof device.token !== 'function') {
    throw new TypeError('relayTokenSource: a device token source is required (token)');
  }
  if (relay === null || typeof relay !== 'object' || typeof relay.register !== 'function') {
    throw new TypeError('relayTokenSource: the publisher\'s relay is required (register)');
  }
  if (typeof endpoint !== 'string' || endpoint === '') {
    throw new TypeError(
      'relayTokenSource: the publisher\'s relay endpoint must be named: the app registers with a relay the ' +
        'publisher holds, and which one is the wiring\'s to state rather than this module\'s to default',
    );
  }
  return Object.freeze({
    token: async () => {
      const phone = await device.token();
      // The device's own refusal is its own answer (`no-token` from the SDK,
      // say) and keeps its code, so the screen's phrase is the one written for
      // that fact. A device that throws is not caught here either:
      // `readPushToken` reports it once, as the broken device it is.
      if (!tokenIn(phone)) {
        return isPlainObject(phone) && phone.ok === false && typeof phone.code === 'string'
          ? refuse(phone.code, 'token')
          : refuse(TOKEN_CODES.TOKEN_FAILED, 'token');
      }
      let registered;
      try {
        registered = await relay.register({ endpoint, deviceToken: phone.token });
      } catch {
        // Deliberately not carrying the error through: a transport's own
        // message can name the endpoint, a header or a body, and the code is
        // the report (the same rule `readPushToken` follows).
        return refuse(RELAY_CODES.RELAY_FAILED, 'relay');
      }
      if (!tokenIn(registered)) return refuse(RELAY_CODES.RELAY_FAILED, 'relay');
      // §3.3's "a publisher token INSTEAD": a relay that answers the phone's
      // own address back has not registered this installation -- it has echoed
      // the one thing the relay exists to keep out of the registration -- so
      // nothing is registered rather than a device token written into the
      // mailbox.
      if (registered.token === phone.token) return refuse(RELAY_CODES.RELAY_FAILED, 'relay');
      return Object.freeze({ ok: true, token: registered.token });
    },
  });
}
