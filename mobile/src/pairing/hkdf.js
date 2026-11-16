// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// HKDF-SHA-256 (mobile plan, Task P6.8; the pairing plan's §8).
//
// **Why it is here and not in `shared/`.** It has one caller -- the delivery
// envelope's key derivation -- and one counterpart, `crypto/hkdf`'s
// `hkdf.Key` in `internal/budget/token`'s `deliveryAEAD`. Moving it into the
// shared package would be a licence change (D311) and a second wire-format
// surface for one call, so it lives beside the envelope that uses it.
//
// **Why it is written rather than imported.** The same reason
// `src/mailbox/sha256.js` exists: Metro resolves no `node:` builtin and the
// suite runs with no `node_modules`, so a module the tests load imports
// nothing. This one is a thin composition of HMAC-SHA-256, which the tree
// already has and already tests, so the new code is the derivation and not the
// hash.
//
// The three parameters are the CLI's, in the CLI's order and with its own
// defaults: `hkdf.Key(sha256.New, shared, nil, "fugaro-pairing-v1", 32)`. A
// salt of `nil` means HashLen zero bytes (RFC 5869 §2.2), and the info string
// is the label that makes this key agreement's key this one and not a key some
// other use of the same ECDH secret would derive (§8).

import { hmacSha256 } from '../mailbox/sha256.js';

/** SHA-256's output length: HKDF's `HashLen`, and the salt's length when no
 * salt is given. */
export const HKDF_HASH_LENGTH = 32;

/** How this module refuses. A length no single block can answer for is the one
 * thing a caller can get wrong, and it is refused rather than truncated: a
 * short key is a key nobody agreed to. */
export const HKDF_CODES = Object.freeze({
  BAD_INPUT: 'bad-input',
  TOO_LONG: 'too-long',
});

/**
 * Derive `length` bytes from `ikm` with SHA-256 as the hash, no salt, and
 * `info` as the context label (RFC 5869).
 *
 * `extract` then `expand`, the construction's two steps and nothing else: the
 * PRK is HMAC over the input keying material with a zero-filled salt, and each
 * output block is HMAC over the previous block, the info string and the
 * counter. The counter is one byte and the loop is bounded by it, which is why
 * a length above 255 * 32 bytes is refused rather than wrapped -- a wrap would
 * silently derive the first block twice and hand back a key that is not the
 * one the other end derived.
 */
export function hkdfSha256(ikm, info, length = HKDF_HASH_LENGTH) {
  if (!(ikm instanceof Uint8Array) || !(info instanceof Uint8Array)) {
    return Object.freeze({ ok: false, code: HKDF_CODES.BAD_INPUT });
  }
  if (!Number.isInteger(length) || length <= 0 || length > 255 * HKDF_HASH_LENGTH) {
    return Object.freeze({ ok: false, code: HKDF_CODES.TOO_LONG });
  }
  const salt = new Uint8Array(HKDF_HASH_LENGTH);
  const prk = hmacSha256(salt, ikm);
  const out = new Uint8Array(length);
  let previous = new Uint8Array(0);
  let filled = 0;
  for (let counter = 1; filled < length; counter += 1) {
    const input = new Uint8Array(previous.length + info.length + 1);
    input.set(previous, 0);
    input.set(info, previous.length);
    input[input.length - 1] = counter;
    previous = hmacSha256(prk, input);
    const take = Math.min(HKDF_HASH_LENGTH, length - filled);
    out.set(previous.subarray(0, take), filled);
    filled += take;
  }
  return Object.freeze({ ok: true, bytes: out });
}
