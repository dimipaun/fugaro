// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The phone's key pair, and the code the operator reads off its screen
// (mobile plan, Task P6.8; the pairing plan's §2 (e), §7.1 step 5, §7.2, §8,
// and decision 3).
//
// **What the phone holds.** An X25519 key pair, generated **here, on the
// phone**, and never anywhere else: the private half never leaves the process
// and is never written to the database, and the public half is the only thing
// that goes on the wire -- into `pairing/$uid/request` as `key`, and into the
// delivery envelope's AAD, where it is what stops a payload sealed for one
// phone from being opened by another phone that shares the node (§8).
//
// **The code is the key's fingerprint, not an identity.** Decision 3: eight
// Crockford base32 characters over the first five bytes of SHA-256 of the
// **decoded** key, printed `XXXX-XXXX`. It authorises nothing -- pairing is
// discovery (#787) -- and what it defeats is a photographed QR paired in place
// of the real phone, whose owner alone can make their screen show this code
// (M3). It is derived per pairing and never stored, by either end: the CLI
// computes the same value from the request node (`Fingerprint`), and the phone
// computes it from the key it just made. It is a hash of a public key, so it
// is not a secret -- §4.5 lets the CLI print it -- and it is the one string on
// the screen the operator compares.
//
// **The algorithm is the primitive, and the primitive is a port.** The tree's
// rule (`mobile/README.md`, `src/licence/index.js`): the suite runs in a fresh
// clone with no `node_modules`, so a module a test loads imports nothing. This
// file therefore takes X25519 as an argument -- `{ getPublicKey(secret) }`,
// exactly the two calls this protocol needs -- and `index.js`, which no test
// imports, plugs the app's own dependency into it. That is also why the
// app's X25519 and the CLI's `crypto/ecdh` are cross-checked by a vector
// rather than by sharing code: `envelope.test.js` opens bytes the Go command
// sealed.

import { encodeBase64url, decodeBase64url, BASE64URL_CODES } from './base64url.js';
import { sha256 } from '../mailbox/sha256.js';

/** The phone's public key: 32 raw bytes. `token.DeviceKeySize` on the CLI. */
export const DEVICE_KEY_SIZE = 32;

/** The X25519 secret is 32 bytes too -- `crypto/ecdh`'s own size, and the size
 * the driver below draws. */
export const DEVICE_SECRET_SIZE = 32;

/** How the code is printed: two groups of four (decision 3). */
export const CODE_LENGTH = 8;

/** How much of the digest the code uses: eight five-bit groups are exactly five
 * bytes, which is `token.deviceCodeBytes`. It is derived rather than written
 * again, so the two cannot drift. */
export const CODE_BYTES = (CODE_LENGTH * 5) / 8;

/** Crockford base32: the digits and the letters without I, L, O and U, so that
 * no character the code prints can be read as another one. The CLI's
 * `deviceCodeAlphabet` is this string, in this order. */
export const CODE_ALPHABET = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';

/** How this module refuses. */
export const KEY_CODES = Object.freeze({
  NOT_AN_OBJECT: 'not-an-object',
  NOT_A_STRING: 'not-a-string',
  NOT_TEXT: 'not-text',
  BAD_LENGTH: 'bad-length',
  BAD_ALPHABET: 'bad-alphabet',
  NOT_CANONICAL: 'not-canonical',
  NOT_CANONICAL_POINT: 'not-canonical-point',
  BAD_SECRET: 'bad-secret',
  BAD_PORT: 'bad-port',
});

function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * Read a device key from the one spelling a request node may carry: unpadded
 * base64url of `DEVICE_KEY_SIZE` bytes, 43 characters (§2 (e)).
 *
 * **Why the bytes and not the text.** Everything downstream hashes or binds
 * the *decoded* key -- `fingerprint` here, the envelope's AAD in
 * `envelope.js` -- so a caller can never accidentally bind the spelling.
 *
 * The top bit of the last byte is refused, and that refusal is load-bearing
 * rather than pedantic: it is X25519's discarded bit, so `crypto/ecdh` masks
 * it away and a key with it set combines to the same secret -- which means the
 * CLI would seal a payload whose AAD carries bytes the phone's own
 * `getPublicKey` never spells, and the phone could never open it. The CLI
 * refuses the same byte for the same reason (`DecodeDeviceKey`), so a request
 * node carrying one fails at both ends.
 */
export function decodeDeviceKey(text) {
  if (typeof text !== 'string') return refuse(KEY_CODES.NOT_A_STRING, 'key');
  const decoded = decodeBase64url(text, DEVICE_KEY_SIZE);
  if (!decoded.ok) {
    // The base64 refusals are this module's refusals, renamed once so a caller
    // switching on `KEY_CODES` never has to know about `BASE64URL_CODES`.
    switch (decoded.code) {
      case BASE64URL_CODES.BAD_LENGTH:
      case BASE64URL_CODES.EMPTY:
        return refuse(KEY_CODES.BAD_LENGTH, 'key');
      case BASE64URL_CODES.NOT_CANONICAL:
        return refuse(KEY_CODES.NOT_CANONICAL, 'key');
      default:
        return refuse(KEY_CODES.BAD_ALPHABET, 'key');
    }
  }
  if ((decoded.bytes[DEVICE_KEY_SIZE - 1] & 0x80) !== 0) {
    return refuse(KEY_CODES.NOT_CANONICAL_POINT, 'key');
  }
  return Object.freeze({ ok: true, key: decoded.bytes });
}

/** Write a public key in the one spelling the request node uses. Only the
 * writer of the request calls it: a reader never re-spells what it read. */
export function encodeDeviceKey(key) {
  if (!(key instanceof Uint8Array) || key.length !== DEVICE_KEY_SIZE) {
    throw new TypeError('encodeDeviceKey takes the device key’s 32 bytes');
  }
  return encodeBase64url(key);
}

/**
 * The code the operator and the phone compare: decision 3's fingerprint of the
 * **decoded** key.
 *
 * The arithmetic is Crockford's, not a taste: the first five bytes of the
 * digest are one big-endian 40-bit integer, which is exactly eight five-bit
 * groups, so the code uses every bit the digest gives it and the alphabet's
 * index is the group's value. `internal/budget/token`'s `Fingerprint` walks
 * the same bits in the same order, and the vector in `envelope.test.js` is the
 * proof that the two agree rather than an argument that they should.
 */
export function fingerprint(key) {
  if (!(key instanceof Uint8Array) || key.length !== DEVICE_KEY_SIZE) {
    throw new TypeError('fingerprint takes the device key’s 32 bytes');
  }
  const digest = sha256(key);
  let bits = 0;
  for (let i = 0; i < CODE_BYTES; i += 1) bits = bits * 256 + digest[i];
  const chars = new Array(CODE_LENGTH);
  for (let i = CODE_LENGTH - 1; i >= 0; i -= 1) {
    chars[i] = CODE_ALPHABET[bits % 32];
    bits = Math.floor(bits / 32);
  }
  return chars.slice(0, CODE_LENGTH / 2).join('') + '-' + chars.slice(CODE_LENGTH / 2).join('');
}

/**
 * Generate the phone's key pair, from the driver `index.js` hands in.
 *
 * `x25519` is the app's dependency as this protocol needs it: one call,
 * `getPublicKey(secret)`, which is the only half of the primitive this file
 * uses. (The agreement itself is `envelope.js`'s, and it is the same port.)
 *
 * `random` is the draw: a port rather than `crypto.getRandomValues`, for the
 * tree's own reason -- the driver is a device concern and the test is
 * hermetic. It must answer exactly `DEVICE_SECRET_SIZE` bytes, and a driver
 * that answers anything else is refused rather than padded or truncated: a
 * secret of the wrong size is a key nobody agreed to.
 *
 * The private half is returned to the caller and stored nowhere by this
 * module: the caller holds it for the length of one pairing, and it dies with
 * the attempt.
 */
export function generateKeyPair({ x25519, random }) {
  if (!isDriver(x25519, 'getPublicKey')) return refuse(KEY_CODES.BAD_PORT, 'x25519');
  if (typeof random !== 'function') return refuse(KEY_CODES.BAD_PORT, 'random');
  let secret;
  try {
    secret = random(DEVICE_SECRET_SIZE);
  } catch {
    return refuse(KEY_CODES.BAD_SECRET, 'random');
  }
  if (!(secret instanceof Uint8Array) || secret.length !== DEVICE_SECRET_SIZE) {
    return refuse(KEY_CODES.BAD_SECRET, 'random');
  }
  let publicKey;
  try {
    publicKey = x25519.getPublicKey(secret);
  } catch {
    return refuse(KEY_CODES.BAD_SECRET, 'x25519');
  }
  if (!(publicKey instanceof Uint8Array) || publicKey.length !== DEVICE_KEY_SIZE) {
    return refuse(KEY_CODES.BAD_SECRET, 'x25519');
  }
  return Object.freeze({ ok: true, secret, publicKey });
}

/** Whether a value is a port carrying `name` as a function. A port that is
 * half there is refused before it is called, so the refusal is this module's
 * and not a `TypeError` from three frames down. */
export function isDriver(port, name) {
  return port !== null && typeof port === 'object' && typeof port[name] === 'function';
}
