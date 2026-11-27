// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The message tag, in the app (mobile plan, Task P3.6; P6.3's schema,
// functional-spec-reconciliation.md §3.5, D171, and J5b's device signature,
// O6).
//
// **What this file is, and what it deliberately is not.** It is the app's
// signing and verification over the schema's `canonicalBytes` -- the one
// serialisation both ends share, which the Go consumer reimplements. It is not
// a second schema: the field set, the bounds, the direction vocabulary and the
// byte string all come from `shared/mailbox-schema`, which the app imports for
// exactly this.
//
// **The two schemes a tag can be, and who chooses.** D171's per-pairing MAC
// (`macOf`) is the old way and still the only one an older phone knows; O6's
// device signature (`sigOf`) is what a phone paired the new way writes, and the
// device key it signs with never leaves the session port. Which one a record
// must carry is **the device node's** answer at the consumer (a node holding a
// public key takes signatures only), so a writer that could choose per message
// would be a writer that could downgrade itself: `client.js` fixes the scheme
// once, at the wiring, from whether it has a session. The MAC's arithmetic is
// this app's own (`sha256.js`: Metro resolves no `node:` builtin and Hermes has
// no `Buffer`); the signature's is the session's, which is the whole reason
// `sigOf` takes a signer and not a key.
//
// **The direction is the reader's, not the record's.** The owner's ruling of
// 2026-10-18 put `direction` (one of `DIRECTIONS`: inbox, outbox, heartbeat)
// inside the covered bytes, so a tag made for one node does not verify in
// another and a message reflected from the inbox into the outbox -- or a
// heartbeat presented as a message -- fails. The caller therefore supplies it
// from the node it read (or is about to write); `canonicalBytes` throws if the
// record carries a `direction` of its own, and `verifyMac` answers `false` for
// such a record rather than trusting it.
//
// **The other direction: what the phone verifies, and with what** (J5c, O12).
// The outbox and the heartbeat are the CONSUMER's records, so the phone is the
// reader there and the consumer is the writer. A phone paired the old way
// checks them with its per-pairing key (`verifyMac`, D171); a phone paired the
// new way holds no such key at all and checks them with the consumer's public
// key the pairing returned and the app pinned (`verifySig`, O12). The
// primitive is a port -- `(signature, message, publicKey)` -- because the
// consumer's private half is in Cloud KMS and never anywhere near the app, and
// because the same port is what the licence verifier takes.
//
// **Two asymmetries, both on purpose.**
//
//   * `macOf` and `sigOf` -- the writers' -- **throw** for a key or a signer
//     that is not one, a direction that is not one of the three, or a record the
//     serialisation refuses. A writer that coerced `String(undefined)` into a
//     key would produce messages nobody can verify, and the failure would
//     surface at the other end.
//   * `verifyMac` and `verifySig` -- the readers' -- **never throw** and answer
//     `false` for everything they cannot decide: a record that is not an object,
//     a `mac` or a `sig` that is not a string, a key that is not a string, a
//     spelling that is not canonical, a field the serialisation refuses, a
//     record carrying its own direction, a verifying port that threw. They are
//     called on values that arrived from outside the app, in the phone's message
//     loop, and a throw there is a way to stop the loop with a record. This is
//     the same shape P6.3's Node `verifyMac` has, and the two are held together
//     by `vectors.json`.
//
// **The comparison is constant-time** over equal-length tags: a caller that
// verifies tags an attacker chose would otherwise learn one byte per attempt
// from how long the answer took. A length mismatch answers immediately, which
// leaks the length -- a tag is 64 hex characters or it is not a tag, and the
// schema's own shape rule is public.
//
// **No key here is read, stored, derived, logged or defaulted.** The key
// arrives as an argument, leaves as a digest, and the one caller that has it
// (`client.js`) holds it for the duration of a callback inside
// `SecretStore.use` -- never in a field, never in a report, never in a fixture.
// The device key is not even an argument: `sigOf` takes the session's own
// `signMessage`, so the material stays in the session and this file sees a
// signature.

import { SIG_CHARS, canonicalBytes } from '../../../shared/mailbox-schema/index.js';
// The pairing module's own unpadded base64url, reached as a FILE rather than
// through `pairing/index.js`: the door pulls the pairing screen and the session,
// and this suite runs in a fresh clone with no `node_modules` (`mobile/README.md`).
// `base64url.js` itself imports nothing, which is why it can be shared; writing a
// second encoder here is the "second spelling of one value" the tree refuses.
// Its decoder is strict (the trailing bits must be zero), which is the rule the
// reader of a signature needs for the same reason the writer of one does: a
// second spelling of a signature is a second value to verify.
import { decodeBase64url, encodeBase64url } from '../pairing/base64url.js';
import { hmacSha256, toHex, utf8Bytes } from './sha256.js';

/** The bytes `SIG_CHARS` characters are the one spelling of: an Ed25519
 * signature (O6), which is C1's `DeviceSignatureSize`. The schema keeps its own
 * copy private, and this is the length a signer's answer is held to. */
const DEVICE_SIGNATURE_BYTES = 64;

/** The bytes the consumer's pinned public key is: C1's pair response carries it
 * as the 32 raw bytes of an Ed25519 key in unpadded base64url (O12), which is
 * the spelling `internal/mailbox/kmssigner.go` publishes. A string of another
 * length is not a key this file hands the primitive. */
const CONSUMER_PUBLIC_KEY_BYTES = 32;

/**
 * The tag a record carries: HMAC-SHA-256 over `canonicalBytes(record,
 * direction)`, lowercase hex (§3.5).
 *
 * @param {object} record a validated message or heartbeat
 * @param {string} key the per-pairing key (D171) -- the phone's, from platform
 *   secure storage. It is a string of UTF-8 bytes; anything else throws.
 * @param {string} direction one of `DIRECTIONS`: the node the record is (to
 *   be) stored in, which is inside the tagged bytes
 * @returns {string} 64 lowercase hex characters
 * @throws {TypeError} for a key that is not a string, a direction that is not
 *   one of the three, or a record `canonicalBytes` refuses
 */
export function macOf(record, key, direction) {
  if (typeof key !== 'string') throw new TypeError('macOf: the key must be a string');
  // The bytes and the key are both UTF-8: `canonicalBytes` returns a string
  // whose UTF-8 encoding is what is signed, and the key is whatever the
  // pairing stored, encoded the same way at both ends.
  return toHex(hmacSha256(utf8Bytes(key), utf8Bytes(canonicalBytes(record, direction))));
}

/**
 * The tag a record a phone paired the new way writes: the device's signature
 * over `canonicalBytes(record, direction)`, in the one spelling a signature has
 * -- unpadded base64url of the 64 bytes Ed25519 produces (O6).
 *
 * The signature is made **by the session port and not here**: `sign` is C2's
 * `signMessage`, bound to the session, and the device key never leaves it. That
 * is the whole difference from `macOf`, which is handed the key because the app
 * itself holds the pairing's key.
 *
 * @param {object} record a validated message or heartbeat, without its tag
 * @param {(bytes: Uint8Array) => Promise<Uint8Array>} sign the session's
 *   `signMessage`: it signs the bytes it is given with the device key, or
 *   refuses (a session that has signed out, a key that is gone)
 * @param {string} direction one of `DIRECTIONS`: the node the record is (to
 *   be) stored in, which is inside the signed bytes
 * @returns {Promise<string>} `SIG_CHARS` characters of unpadded base64url
 * @throws {TypeError} for a signer that is not a function, a record
 *   `canonicalBytes` refuses, or a session that did not answer with a
 *   device signature -- a writer that wrote something nobody can verify would
 *   fail at the other end instead
 */
export async function sigOf(record, sign, direction) {
  if (typeof sign !== 'function') throw new TypeError('sigOf: a signing function is required');
  // The bytes are UTF-8, exactly as they are for the MAC: one serialisation,
  // one byte string, whichever scheme covers it.
  const signature = await sign(utf8Bytes(canonicalBytes(record, direction)));
  if (!(signature instanceof Uint8Array) || signature.length !== DEVICE_SIGNATURE_BYTES) {
    throw new TypeError('sigOf: the session did not answer with a device signature');
  }
  return encodeBase64url(signature);
}

/**
 * Does this record carry the tag its own bytes produce under `key`?
 *
 * It answers `false` -- never throws -- for anything that cannot carry one: a
 * value that is not an object, a `mac` that is not a string, a key that is not
 * a string, a record holding a value the serialisation refuses, and a
 * direction that is not one of the three.
 *
 * @param {unknown} record a message or heartbeat as it arrived
 * @param {string} key the per-pairing key (D171)
 * @param {string} direction the node the record was READ from -- never a value
 *   the record supplies, because a tag made for another node does not verify
 *   here
 * @returns {boolean} true only when the record's `mac` is its own tag under key
 */
export function verifyMac(record, key, direction) {
  if (typeof key !== 'string') return false;
  if (record === null || typeof record !== 'object') return false;
  // Read `mac` once, and only when it is a string: everything else is decided
  // by `macOf` one call down, including the direction and the serialisation's
  // own refusals, so this file states no rule the schema already states.
  let offered;
  try {
    offered = record.mac;
  } catch {
    // A Proxy can throw from any trap. That is a `false`, not an exception out
    // of the phone's message loop.
    return false;
  }
  if (typeof offered !== 'string') return false;
  let expected;
  try {
    expected = macOf(record, key, direction);
  } catch {
    return false;
  }
  return sameTag(offered, expected);
}

// sameTag compares two tags without an early exit on the first difference, so
// the time the answer takes does not depend on how much of an attacker's tag
// was right. A length mismatch returns at once: the length is not a secret
// (the schema fixes it, and a tag that is not the right shape is not a tag).
function sameTag(offered, expected) {
  if (offered.length !== expected.length) return false;
  let difference = 0;
  for (let i = 0; i < offered.length; i++) {
    difference |= offered.charCodeAt(i) ^ expected.charCodeAt(i);
  }
  return difference === 0;
}

/**
 * Does this record carry the consumer's own signature, under the public key
 * this phone pinned?
 *
 * The reader's half of O12, and `verifyMac`'s twin: a phone paired the new way
 * holds no MAC key at all, so what it checks the consumer's replies and its
 * heartbeat with is the consumer's public key, which the pairing returned and
 * the app stored. `sigOf`'s asymmetry is the other one -- the phone SIGNS with
 * a session and VERIFIES with a pinned key, so the primitive arrives as a port
 * here and as a signer there, and neither of them is key material this file
 * holds.
 *
 * It answers `false` -- never throws -- for everything it cannot decide: a
 * value that is not an object, a `sig` that is not a string, a key that is not
 * a string, a spelling that is not canonical, a record the serialisation
 * refuses, a direction that is not one of the three, and a port that throws or
 * answers anything but `true`. It is called on values that arrived from outside
 * the app, in the phone's message loop, and a throw there is a way to stop the
 * loop with a record. The one thing it does throw for is a `verify` that is not
 * a function: that is the wiring's fault and not a record's, it is caught at
 * construction rather than per message, and a reader that answered `false` for
 * it would be a phone that silently drops every reply.
 *
 * The port is the app's Ed25519 in the order the licence verifier takes it --
 * `(signature, message, publicKey)`, all three as the raw bytes -- and it is
 * the same shape there, so the app has one Ed25519 port and not two.
 *
 * @param {unknown} record a message or heartbeat as it arrived
 * @param {string} publicKey the consumer's public key, as the pairing returned
 *   it: unpadded base64url of its 32 raw bytes
 * @param {(signature: Uint8Array, message: Uint8Array, publicKey: Uint8Array) => boolean} verify
 *   the Ed25519 port
 * @param {string} direction the node the record was READ from -- never a value
 *   the record supplies, because a signature made for another node does not
 *   verify here
 * @returns {boolean} true only when the record's `sig` is the consumer's own
 *   over the record's canonical bytes
 * @throws {TypeError} for a `verify` that is not a function
 */
export function verifySig(record, publicKey, verify, direction) {
  if (typeof verify !== 'function') throw new TypeError('verifySig: a verifying function is required');
  if (typeof publicKey !== 'string') return false;
  if (record === null || typeof record !== 'object') return false;
  // Read `sig` once, and only when it is a string: everything else is decided
  // by the decoders one call down, including the direction and the
  // serialisation's own refusals, so this file states no rule the schema or
  // `base64url.js` already states.
  let offered;
  try {
    offered = record.sig;
  } catch {
    // A Proxy can throw from any trap. That is a `false`, not an exception out
    // of the phone's message loop.
    return false;
  }
  if (typeof offered !== 'string') return false;
  const signature = decodeBase64url(offered, DEVICE_SIGNATURE_BYTES);
  if (!signature.ok) return false;
  const key = decodeBase64url(publicKey, CONSUMER_PUBLIC_KEY_BYTES);
  if (!key.ok) return false;
  let message;
  try {
    message = utf8Bytes(canonicalBytes(record, direction));
  } catch {
    return false;
  }
  try {
    // `=== true` and not truthiness: a port that answered a truthy non-boolean
    // answered something this file did not ask for, and a record is admitted on
    // the primitive's own verdict and on nothing else.
    return verify(signature.bytes, message, key.bytes) === true;
  } catch {
    return false;
  }
}
