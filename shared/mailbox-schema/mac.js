// The message tag: HMAC-SHA-256 over `canonicalBytes`, in the one file that can
// compute it (mobile plan, Task P6.3; functional-spec-reconciliation.md §3.5,
// D171).
//
// **Nothing imports this file on the way to the schema, and that is the whole
// reason it is a file.** This package has two consumers (D119): the console,
// which is Electron and therefore Node, and the phone, which is React Native.
// Metro resolves no `node:` builtin and Hermes has no Buffer type, so a static
// import of Node's crypto in `index.js` would not merely cost the phone the
// tag — it would make the WHOLE schema unimportable there, `validateMessage`
// and `canonicalBytes` included, which are pure functions over values and which
// the phone needs most. So the boundary is drawn here:
//
//   - `index.js` imports nothing at all; the phone imports it and gets the
//     validator, the heartbeat rule and the canonical serialisation;
//   - the phone implements HMAC-SHA-256 itself — SHA-256 and the HMAC
//     construction over bytes, a few hundred lines of pure integer arithmetic —
//     and tests it against `vectors.json`, the byte-for-byte contract both ends
//     share, including the obviously fake key in it;
//   - this file is the Node implementation: what the tests, the vectors
//     generator and any Node consumer (the console, P4.1's `fugaro mailbox`)
//     use.
//
// A tag is only as good as the key it is computed with, and that key is D171's
// **per-pairing** key: the phone's from secure storage, the consumer's from the
// OS keychain. Nothing here reads, stores, derives, logs or defaults one — the
// key arrives as an argument and leaves as a digest, and every value this file
// is tested against is signed with a placeholder that is asserted to be one.

import { createHmac, timingSafeEqual } from 'node:crypto';

import { canonicalBytes } from './index.js';

/**
 * macOf is the tag a record carries: HMAC-SHA-256 over canonicalBytes(record),
 * hex-encoded, lowercase (§3.5).
 *
 * @param {object} record a validated message or heartbeat
 * @param {string} key the per-pairing key (D171): the phone's from secure
 *   storage, the consumer's from the OS keychain. It is a string of UTF-8
 *   bytes; passing anything else is a programming error and throws.
 * @param {string} direction one of DIRECTIONS (`inbox`, `outbox`, `heartbeat`):
 *   the node the record is written to, which is inside the tagged bytes
 * @returns {string} MAC_MAX lowercase hex characters
 */
export function macOf(record, key, direction) {
  if (typeof key !== 'string') {
    throw new TypeError('macOf: the key must be a string');
  }
  const bytes = Buffer.from(canonicalBytes(record, direction), 'utf8');
  return createHmac('sha256', Buffer.from(key, 'utf8')).update(bytes).digest('hex');
}

/**
 * verifyMac decides whether a record carries the tag its own bytes produce
 * under key.
 *
 * It answers `false` — never throws — for anything that cannot carry one: a
 * value that is not a plain object, a `mac` that is not a string, a key that is
 * not a string, and a record holding a value canonicalBytes refuses. The
 * comparison is timing-safe, because a caller that verifies tags an attacker
 * can choose would otherwise leak the tag one byte at a time.
 *
 * @param {unknown} record a message or heartbeat as it arrived
 * @param {string} key the per-pairing key
 * @param {string} direction the node the record was READ from — never a value
 *   the record supplies: a tag made for another node does not verify here
 * @returns {boolean} true only when the record's `mac` is its own tag under key
 */
export function verifyMac(record, key, direction) {
  if (typeof key !== 'string') return false;
  // A value that is not a plain object carries no tag to compare: the object
  // test is canonicalBytes', one call down, and it throws — which is the same
  // answer, and one implementation of the question rather than two.
  if (record === null || typeof record !== 'object' || typeof record.mac !== 'string') {
    return false;
  }
  let expected;
  try {
    expected = macOf(record, key, direction);
  } catch {
    return false;
  }
  const got = Buffer.from(record.mac, 'utf8');
  const want = Buffer.from(expected, 'utf8');
  if (got.length !== want.length) return false;
  return timingSafeEqual(got, want);
}
