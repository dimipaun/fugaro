// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// Unpadded base64url, strictly decoded (mobile plan, Task P6.8; the pairing
// plan's §2 (e) and §8).
//
// **Why this is written rather than imported.** `atob` is a browser global
// that React Native's Hermes does not guarantee, and it decodes the *standard*
// alphabet with padding -- not this one. The tree's precedent is
// `src/mailbox/sha256.js`: what the platform does not reliably have, written
// where it is needed, with nothing behind it. It imports nothing at all, so
// every test that loads it runs in a fresh clone with no `node_modules`.
//
// **What strict means, and why it is the whole point.** Every value this app
// reads in this spelling is bound into something unforgeable: the phone's
// public key is hashed into the code the operator types and into the delivery
// envelope's AAD, the pairing uid is the AAD's first field, and the MAC key is
// the material that signs every message. 32 bytes is 43 characters whose last
// one carries four significant bits and two this encoding discards, so three
// other last characters decode to the same 32 bytes -- four spellings of one
// key, which is exactly what `internal/budget/token`'s `DecodeDeviceKey`
// refuses with `base64.RawURLEncoding.Strict()`. The rule here is the
// same one, byte for byte, because the two ends must refuse the same strings:
// a phone that accepted a non-canonical spelling would hash bytes the CLI
// never did, and show a code that cannot match.

/** How this module refuses. Same shape as `shared/url`'s, and the same reason:
 * a refusal is a value a caller switches on, never a throw. */
export const BASE64URL_CODES = Object.freeze({
  NOT_A_STRING: 'not-a-string',
  EMPTY: 'empty',
  ALPHABET: 'alphabet',
  BAD_LENGTH: 'bad-length',
  PADDED: 'padded',
  NOT_CANONICAL: 'not-canonical',
});

const ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_';

/** The value of each character, or -1. Built once, so a decode is a lookup. */
const VALUES = (() => {
  const table = new Int8Array(128).fill(-1);
  for (let i = 0; i < ALPHABET.length; i += 1) table[ALPHABET.charCodeAt(i)] = i;
  return table;
})();

function refuse(code) {
  return Object.freeze({ ok: false, code });
}

/** The number of characters `bytes` bytes are written as, unpadded. */
export function encodedLength(bytes) {
  return Math.ceil((bytes * 8) / 6);
}

/**
 * Decode `text` as exactly `bytes` bytes of unpadded base64url.
 *
 * `bytes` is required rather than optional: a decoder without a length is a
 * decoder that will accept a key of any size, and every length in this
 * protocol is fixed (`DeviceKeySize`, `pairingUIDBytes`, `KeySize`). `padding`
 * is refused rather than stripped -- the CLI's spelling has none, and a padded
 * string is a second spelling of one value.
 */
export function decodeBase64url(text, bytes) {
  if (typeof text !== 'string') return refuse(BASE64URL_CODES.NOT_A_STRING);
  if (text.length === 0) return refuse(BASE64URL_CODES.EMPTY);
  const chars = encodedLength(bytes);
  if (text.length !== chars) return refuse(BASE64URL_CODES.BAD_LENGTH);
  const out = new Uint8Array(bytes);
  let acc = 0;
  let bits = 0;
  let at = 0;
  for (let i = 0; i < chars; i += 1) {
    const code = text.charCodeAt(i);
    // A code unit above the ASCII table is not in the alphabet, and the table
    // is indexed rather than searched so that this is one comparison.
    const value = code < 128 ? VALUES[code] : -1;
    if (value < 0) {
      // `=` is its own refusal: a padded string is the standard encoding's
      // spelling, and saying so is more useful than "not in the alphabet".
      return refuse(code === 0x3d ? BASE64URL_CODES.PADDED : BASE64URL_CODES.ALPHABET);
    }
    acc = (acc << 6) | value;
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      out[at] = (acc >> bits) & 0xff;
      at += 1;
      acc &= (1 << bits) - 1;
    }
  }
  // The bits left over are the encoding's own discarded ones: they must be
  // zero, or this string is not the canonical spelling of these bytes and
  // three other strings decode to the same value.
  if (acc !== 0) return refuse(BASE64URL_CODES.NOT_CANONICAL);
  return Object.freeze({ ok: true, bytes: out });
}

/**
 * Write `bytes` in the one spelling this protocol uses: unpadded base64url.
 *
 * It exists for the request node the phone writes and for tests that build a
 * value the CLI would have built; nothing that *reads* a wire value calls it,
 * because re-encoding a value it just decoded would make this module the
 * second spelling of it.
 */
export function encodeBase64url(bytes) {
  let out = '';
  let acc = 0;
  let bits = 0;
  for (let i = 0; i < bytes.length; i += 1) {
    acc = (acc << 8) | bytes[i];
    bits += 8;
    while (bits >= 6) {
      bits -= 6;
      out += ALPHABET[(acc >> bits) & 0x3f];
    }
    acc &= (1 << bits) - 1;
  }
  // The trailing bits are zero by construction, which is what makes this the
  // encoding `decodeBase64url` accepts.
  if (bits > 0) out += ALPHABET[(acc << (6 - bits)) & 0x3f];
  return out;
}
