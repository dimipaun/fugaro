// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The message tag, in the app (mobile plan, Task P3.6; P6.3's schema,
// functional-spec-reconciliation.md §3.5, D171).
//
// **What this file is, and what it deliberately is not.** It is the app's
// signing and verification: HMAC-SHA-256 (`sha256.js`, the app's own
// arithmetic, because Metro resolves no `node:` builtin and Hermes has no
// `Buffer`) over the schema's `canonicalBytes` -- the one serialisation both
// ends share, which the Go consumer reimplements. It is not a second schema:
// the field set, the bounds, the direction vocabulary and the byte string all
// come from `shared/mailbox-schema`, which the app imports for exactly this.
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
// **Two asymmetries, both on purpose.**
//
//   * `macOf` -- the writer's -- **throws** for a key that is not a string, a
//     direction that is not one of the three, or a record the serialisation
//     refuses. A writer that coerced `String(undefined)` into a key would
//     produce messages nobody can verify, and the failure would surface at the
//     other end.
//   * `verifyMac` -- the reader's -- **never throws** and answers `false` for
//     everything it cannot decide: a record that is not an object, a `mac`
//     that is not a string, a key that is not a string, a field the
//     serialisation refuses, a record carrying its own direction. It is called
//     on values that arrived from outside the app, in the phone's message
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

import { canonicalBytes } from '../../../shared/mailbox-schema/index.js';
import { hmacSha256, toHex, utf8Bytes } from './sha256.js';

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
