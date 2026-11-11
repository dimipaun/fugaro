// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The pairing module's one door (mobile plan, Task P6.8; the pairing plan's
// §7.2).
//
// Everything a screen or the entry needs from this directory, re-exported here
// and nowhere else, so that a caller imports `src/pairing/index.js` and cannot
// reach the pieces in an order the contract does not allow: `session.js` is the
// only thing that sequences the steps, and `openDelivery` without a judged
// payload is a plaintext nothing has read.
//
// **What `createPairing` is for, and what it deliberately does not do.** It
// binds the device's ports -- X25519, the random draw, the clock, the database
// client, the auth exchange and the `AccountStore` -- to the session and the
// screen, so `mobile/index.js` (the one file that knows what a device is) can
// build the whole feature in one call. It names **no dependency of its own**,
// which is this tree's rule for a module a test loads: the suite runs with no
// `node_modules`, so `key.js`, `envelope.js`, `hkdf.js` and `aes.js` all import
// nothing outside the app and take the primitive as an argument. The port
// arrives from the entry, and a missing one is a `TypeError` at construction
// rather than a crash at the first pairing.
//
// **The entry already has the provider.** `@noble/curves` is a declared
// dependency (`dependencies.test.js` pins it at 2.4.0, MIT in the lockfile) and
// the app's licence verifier imports it -- and that same subpath exports
// `x25519`, so the port is `{ getPublicKey, getSharedSecret }` off
// `@noble/curves/ed25519.js`, with `random: () => x25519.utils.randomSecretKey()`
// beside it. No dependency is added by wiring it; the line is the entry's to
// write, and `mobile/README.md`'s export-compliance section names the
// primitives the app now uses.
//
// The camera is the same shape: the screen takes the payload's text, and how a
// device obtains it is the entry's (`screen.js` says why).
//
// **The one thing a caller of the session owes it.** A pairing that nobody
// completes ends on the phone's own clock, and this module starts no timer
// (`session.js` says why): the entry calls `session.tick()` while a pairing is
// `watching`, on whatever tick it already has -- a render, a focus event -- and
// the session says `expired` when the QR's deadline has gone. Nothing else
// needs ticking, and a caller that never ticks leaves a pairing waiting rather
// than breaking it.
//
// `mobile/src/app/first-screen.js` already carries the seam this screen fills
// (`pairingScreen`), and nothing wires it yet: wiring it needs the device's
// ports, which is the entry's line to write.

import { createPairingScreen } from './screen.js';
import { createPairingSession } from './session.js';

export {
  API_KEY_MAX_LENGTH,
  CONFIG_CODES,
  CONFIG_FIELDS,
  databaseHostsFor,
  judgeConfig,
  judgeFieldSet,
  validateDatabaseUrl,
} from './config.js';

export { BASE64URL_CODES, decodeBase64url, encodeBase64url, encodedLength } from './base64url.js';

export {
  CODE_ALPHABET,
  CODE_BYTES,
  CODE_LENGTH,
  DEVICE_KEY_SIZE,
  DEVICE_SECRET_SIZE,
  KEY_CODES,
  encodeDeviceKey,
  fingerprint,
  generateKeyPair,
} from './key.js';

export {
  PAIRING_UID_BYTES,
  PAIRING_UID_PREFIX,
  DEVICE_UID_BYTES,
  DEVICE_UID_PREFIX,
  isDeviceUID,
  isPairingUID,
} from './uid.js';

export {
  PAYLOAD_CODES,
  PAYLOAD_FIELDS,
  PAYLOAD_MAX_LENGTH,
  PAYLOAD_VERSION,
  TOKEN_MAX_LENGTH,
  readScannedPayload,
} from './payload.js';

export {
  DELIVERY_INFO,
  DELIVERY_MIN_ENVELOPE,
  DELIVERY_NODE_FIELDS,
  DELIVERY_VERSION,
  ENVELOPE_CODES,
  ENVELOPE_MAX_CHARS,
  decodeEnvelope,
  deliveryAAD,
  openDelivery,
  readDeliveryNode,
} from './envelope.js';

export {
  DELIVERY_CODES,
  DELIVERY_FIELDS,
  DEVICE_FIELDS,
  DEVICE_TOKEN_MAX_LENGTH,
  readDelivery,
} from './delivery.js';

export {
  SESSION_CODES,
  SESSION_STATES,
  createPairingSession,
  deliveryPath,
  requestPath,
} from './session.js';

export {
  CANCEL_WORD,
  CODE_HINT,
  CODE_LABEL,
  DELIVERED_SENTENCE,
  EXPIRED_SENTENCE,
  IDLE_SENTENCE,
  LABEL_HINT,
  PAIR_WORD,
  PAIRING_TITLE,
  PAYLOAD_HINT,
  SCREEN,
  WATCHING_SENTENCE,
  createPairingScreen,
  refusalSentence,
  stateSentence,
} from './screen.js';

/**
 * The whole pairing feature, over the device's ports.
 *
 * `ports` are `session.js`'s six (`x25519`, `random`, `clock`, `database`,
 * `auth`, `store`) plus the five React primitives `screen.js` takes. Both are
 * required: a caller that forgot one is told at construction.
 */
export function createPairing({ x25519, random, clock, database, auth, store, React, View, Text, TextInput, Pressable }) {
  const session = createPairingSession({ x25519, random, clock, database, auth, store });
  const Screen = createPairingScreen({ React, View, Text, TextInput, Pressable });
  return Object.freeze({ session, Screen });
}
