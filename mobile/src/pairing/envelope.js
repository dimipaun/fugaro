// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The delivery envelope, opened (mobile plan, Task P6.8; the pairing plan's
// §8 and §7.2).
//
// **What arrives.** `internal/budget/token`'s `SealDelivery` writes
//
//     version byte (1) || ephemeral public key (32) || nonce (12) || ciphertext || tag (16)
//     key = HKDF-SHA-256(ECDH(ephemeral_private, phone_public), salt=nil, info="fugaro-pairing-v1")
//     aad = "pairing/" + uid + 0x00 + phone_public_key_bytes (32) + 0x00 + version byte
//
// and this file is the other end of it: agree the same secret, derive the same
// key, verify the tag, and hand back the plaintext bytes. `delivery.js` is what
// judges those bytes; nothing here looks inside them.
//
// **Why the version byte is first and why it is bound into the AAD.** §8's
// reasoning, kept rather than paraphrased: a later format is *refused* rather
// than misread as this one, and binding the version into the AAD means a
// payload cannot be re-spelled as another version's and still open. The uid is
// bound so a delivery cannot be moved to another pairing's node, and the
// phone's own public key is bound so a payload sealed for one phone cannot be
// opened by another phone that shares the node -- which is the photographed-QR
// case the whole envelope exists for.
//
// **The AAD check is the tag check.** §7.2 says the phone must refuse the
// whole payload unless "the AAD names this uid and this key". There is no
// string comparison here and there must not be: the AAD is authenticated data,
// so a delivery built for another uid or another key derives a tag this one
// does not match, and the open fails before a single plaintext byte exists.
// A comparison would be a second check that could disagree with the tag.
//
// **This is a capability, not a boundary** (`seal.go`'s own words, §8, kept):
// it removes the value of a photographed QR or a copied payload and it removes
// no reach. An operator, the launcher, or anyone holding database admin reads
// the delivery node as the CLI wrote it.

import { utf8Bytes } from '../mailbox/sha256.js';

import { AES_KEY_SIZE } from './aes.js';
import { decodeBase64url } from './base64url.js';
import { GCM_CODES, GCM_NONCE_SIZE, GCM_TAG_SIZE, gcmOpen } from './aes-gcm.js';
import { hkdfSha256 } from './hkdf.js';
import { DEVICE_KEY_SIZE, DEVICE_SECRET_SIZE, isDriver } from './key.js';
import { isPairingUID } from './uid.js';

/** The delivery envelope's version byte: `DeliveryEnvelopeVersion` on the CLI.
 * It is first, and it is bound into the AAD. */
export const DELIVERY_VERSION = 1;

/** HKDF's info string: the label that makes this key agreement's key this one
 * and not a key some other use of the same secret would derive (§8). */
export const DELIVERY_INFO = 'fugaro-pairing-v1';

/** The shortest an envelope can be: a version byte, the ephemeral key, a nonce
 * and a tag. A sealed payload of zero bytes is still this long, so anything
 * shorter is not one. */
export const DELIVERY_MIN_ENVELOPE = 1 + DEVICE_KEY_SIZE + GCM_NONCE_SIZE + GCM_TAG_SIZE;

/** The longest envelope this app will look at, in characters of the wire's
 * base64url. A delivery's payload is a few hundred bytes; this is a bound
 * rather than a preference, and it is checked before the string is decoded so
 * a hostile node's value cannot ask for an allocation of its own choosing. */
export const ENVELOPE_MAX_CHARS = 4096;

/** How this module refuses. */
export const ENVELOPE_CODES = Object.freeze({
  NOT_BYTES: 'not-bytes',
  NOT_TEXT: 'not-text',
  TOO_LONG: 'too-long',
  TOO_SHORT: 'too-short',
  BAD_VERSION: 'bad-version',
  BAD_UID: 'bad-uid',
  BAD_KEY: 'bad-key',
  BAD_PORT: 'bad-port',
  /** The agreed secret is all zeros: the ephemeral key on the wire is a
   * low-order point, so the "agreement" is a value anybody can compute and the
   * envelope is not sealed to this phone at all. `crypto/ecdh` refuses the
   * same case at the sealing end. */
  LOW_ORDER_POINT: 'low-order-point',
  /** The tag did not verify. */
  AUTHENTICATION_FAILED: 'authentication-failed',
});

function refuse(code, field) {
  return Object.freeze({ ok: false, code, ...(field === undefined ? {} : { field }) });
}

/**
 * The delivery node's value as the envelope's bytes.
 *
 * The node holds one unpadded base64url string (`internal/rtdb` writes what
 * `SealDelivery` answered), and the bytes it stands for are what
 * `openDelivery` takes. `base64url.js` decodes only a known length -- "a
 * decoder without a length is a decoder that will accept a key of any size" --
 * and here the length is not a constant: the plaintext's is the delivery's.
 * It is derived from the text instead, which is exact for this encoding
 * (unpadded base64url of *n* bytes is `ceil(8n/6)` characters, so
 * `floor(6·chars/8)` is *n* again), and the two things that make a
 * text-derived length unsafe are refused: the string is bounded **before** it
 * is decoded, and a final character carrying non-zero discarded bits is
 * `NOT_CANONICAL`, so no two strings decode to the same bytes.
 */
export function decodeEnvelope(text) {
  if (typeof text !== 'string') return refuse(ENVELOPE_CODES.NOT_TEXT, 'delivery');
  if (text.length === 0) return refuse(ENVELOPE_CODES.TOO_SHORT, 'delivery');
  if (text.length > ENVELOPE_MAX_CHARS) return refuse(ENVELOPE_CODES.TOO_LONG, 'delivery');
  const decoded = decodeBase64url(text, Math.floor((text.length * 6) / 8));
  if (!decoded.ok) return refuse(ENVELOPE_CODES.NOT_BYTES, 'delivery');
  return Object.freeze({ ok: true, bytes: decoded.bytes });
}

/**
 * §8's AAD, in its order, never reordered, truncated, re-encoded or
 * length-prefixed: the literal, the pairing uid, a zero byte, the phone's
 * public key bytes, a zero byte, and the version byte.
 *
 * The uid is the only variable-width part, so the first zero after it is its
 * terminator -- and a uid cannot contain one (`uid.js`'s reason). The key can
 * and routinely does (these are 32 raw public-key bytes), so a zero inside the
 * key is positional data and nothing else: everything after the terminator is
 * fixed width and in a fixed order.
 *
 * It is exported because `envelope.test.js` builds the same AAD to seal a
 * fixture with Node's own crypto, and a second spelling of this layout in a
 * test is a test that would agree with a wrong implementation.
 */
export function deliveryAAD(uid, publicKey, version = DELIVERY_VERSION) {
  const literal = utf8Bytes('pairing/' + uid);
  const aad = new Uint8Array(literal.length + 1 + publicKey.length + 2);
  aad.set(literal, 0);
  aad[literal.length] = 0;
  aad.set(publicKey, literal.length + 1);
  aad[literal.length + 1 + publicKey.length] = 0;
  aad[aad.length - 1] = version;
  return aad;
}

/**
 * Open a delivery envelope sealed for `publicKey`'s private half.
 *
 * `x25519` is the app's dependency as this protocol needs it, and it is the
 * same port `key.js` takes: `getPublicKey(secret)` and
 * `getSharedSecret(secret, publicKey)`. `secret` is the phone's private key
 * -- generated for this pairing, held for its length, and never written
 * anywhere -- and `publicKey` is the public half of the *same* pair, which is
 * what the AAD binds.
 *
 * `uid` is the pairing's name, from the QR. A delivery sealed for another
 * pairing's node fails here, and so does one sealed for another phone.
 */
export function openDelivery({ envelope, secret, publicKey, uid, x25519 }) {
  if (!(envelope instanceof Uint8Array)) return refuse(ENVELOPE_CODES.NOT_BYTES, 'envelope');
  if (envelope.length < DELIVERY_MIN_ENVELOPE) return refuse(ENVELOPE_CODES.TOO_SHORT, 'envelope');
  if (!isPairingUID(uid)) return refuse(ENVELOPE_CODES.BAD_UID, 'uid');
  if (!(secret instanceof Uint8Array) || secret.length !== DEVICE_KEY_SIZE) {
    return refuse(ENVELOPE_CODES.BAD_KEY, 'secret');
  }
  if (!(publicKey instanceof Uint8Array) || publicKey.length !== DEVICE_KEY_SIZE) {
    return refuse(ENVELOPE_CODES.BAD_KEY, 'key');
  }
  if (!isDriver(x25519, 'getSharedSecret')) return refuse(ENVELOPE_CODES.BAD_PORT, 'x25519');
  // The version is checked before anything is derived: a format this build
  // does not know is refused rather than attempted, which is what putting the
  // byte first is for.
  if (envelope[0] !== DELIVERY_VERSION) return refuse(ENVELOPE_CODES.BAD_VERSION, 'envelope');

  const ephemeral = envelope.subarray(1, 1 + DEVICE_KEY_SIZE);
  const nonce = envelope.subarray(1 + DEVICE_KEY_SIZE, 1 + DEVICE_KEY_SIZE + GCM_NONCE_SIZE);
  const body = envelope.subarray(1 + DEVICE_KEY_SIZE + GCM_NONCE_SIZE);
  const ciphertext = body.subarray(0, body.length - GCM_TAG_SIZE);
  const tag = body.subarray(body.length - GCM_TAG_SIZE);

  let shared;
  try {
    shared = x25519.getSharedSecret(secret, ephemeral);
  } catch {
    // The driver refuses a public key that is not a point (the dependency
    // throws; so does `crypto/ecdh`), and such a key is not this envelope's
    // problem to guess at.
    return refuse(ENVELOPE_CODES.BAD_KEY, 'envelope');
  }
  // The agreed secret is an X25519 value like the secret it came from, so it
  // is the same 32 bytes; a driver that answered anything else is not one this
  // protocol agreed with.
  if (!(shared instanceof Uint8Array) || shared.length !== DEVICE_SECRET_SIZE) {
    return refuse(ENVELOPE_CODES.BAD_KEY, 'x25519');
  }
  if (shared.every((byte) => byte === 0)) return refuse(ENVELOPE_CODES.LOW_ORDER_POINT, 'envelope');

  const derived = hkdfSha256(shared, utf8Bytes(DELIVERY_INFO), AES_KEY_SIZE);
  if (!derived.ok) return refuse(ENVELOPE_CODES.BAD_KEY, 'x25519');

  const opened = gcmOpen({
    key: derived.bytes,
    nonce,
    aad: deliveryAAD(uid, publicKey, envelope[0]),
    ciphertext,
    tag,
  });
  if (!opened.ok) {
    return refuse(
      opened.code === GCM_CODES.AUTHENTICATION_FAILED ? ENVELOPE_CODES.AUTHENTICATION_FAILED : ENVELOPE_CODES.BAD_KEY,
      'envelope',
    );
  }
  return Object.freeze({ ok: true, plaintext: opened.plaintext });
}
