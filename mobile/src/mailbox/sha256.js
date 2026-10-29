// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// SHA-256 and HMAC-SHA-256 in the app's own arithmetic (mobile plan, Task
// P3.6; functional-spec-reconciliation.md §3.5, D171).
//
// **Why this file exists.** P6.3 requires a `mac` on every inbox message and
// on the heartbeat, and its node implementation is
// `shared/mailbox-schema/mac.js` -- which imports `node:crypto` and which that
// package's entry point deliberately never imports, because the app's bundler
// (Metro) resolves no `node:` builtin and its engine (Hermes) has no `Buffer`.
// The app has to verify the tags the consumer puts in its outbox (the tension
// (b) resolution: "the client verifies the MAC over each outbox message's id,
// text and timestamp before it speaks a word or acks"), so it computes the tag
// itself. This is that computation: SHA-256's compression function, the
// padding rule, and HMAC's two-block construction, in 32-bit integer
// arithmetic and nothing else.
//
// **This file imports nothing, and the reason is not style.** One `import`
// here -- of `node:crypto`, of a buffer polyfill, of a dependency -- is the
// import that makes the app's bundle unresolvable, and the failure would
// appear as a bundler error on a device and nowhere in CI. `sha256.test.js`
// asserts the file has no import, no require and no `from '...'`, so adding
// one is a failing test. It is the same reason `shared/mailbox-schema/index.js`
// has no import, and the same rule.
//
// **Nothing here holds, reads, derives or logs a key.** `hmacSha256` takes
// bytes and returns bytes: the per-pairing key of D171 arrives from the
// platform's secure store, through `SecretStore.use` (`../accounts/`), and the
// caller that has it is a callback that never returns it.
//
// **The primitive is not the protocol.** These functions know about bytes;
// what the bytes are, which fields they cover and which direction they are
// signed for is `mac.js`, over the schema's `canonicalBytes`. `vectors.json`
// -- the cross-language contract -- is checked there, over the module that
// uses this one.
//
// The arithmetic is the standard one: FIPS 180-4's 64 constants, its eight
// initial state words, its rotation and shift schedule; RFC 2104's ipad/opad
// with SHA-256's 64-byte block. The one decision taken here is the block
// buffer's type (`Int32Array` for the message schedule, so that the additions
// wrap at 32 bits exactly as the standard says) and the fact that the padding
// is materialised rather than streamed, which costs one copy of the message
// and buys a single loop with no special case for the tail.

/** SHA-256's first 32 bits of the fractional parts of the cube roots of the first 64 primes. */
const K = new Int32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]);

/** SHA-256's initial state: the same rule over the square roots of the first eight primes. */
const H = new Uint32Array([
  0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
]);

/** SHA-256's block, in bytes. HMAC's block is the same number, for the same reason. */
const BLOCK = 64;

/** One encoder, made once: `TextEncoder` is a global in Hermes and in Node alike. */
const encoder = new TextEncoder();

/** The lowercase hex alphabet, as a lookup rather than `toString(16)`. */
const HEX = '0123456789abcdef';

/**
 * The UTF-8 bytes of a string.
 *
 * This is the encoding `canonicalBytes`' contract names -- "the bytes, as a
 * string whose UTF-8 encoding is what is signed" -- so it is part of the
 * protocol and not an implementation detail. It is `TextEncoder`'s, which
 * encodes an astral character as one four-byte sequence (a `charCodeAt` loop
 * writes two three-byte sequences and produces a different tag) and a lone
 * surrogate as U+FFFD. A lone surrogate is refused by the schema before
 * anything is signed, so the replacement can never be a difference between the
 * two ends; it is what keeps this function total.
 *
 * @param {unknown} text
 * @returns {Uint8Array}
 */
export function utf8Bytes(text) {
  if (typeof text !== 'string') throw new TypeError('utf8Bytes: a string is required');
  return encoder.encode(text);
}

/**
 * SHA-256, FIPS 180-4.
 *
 * @param {unknown} bytes a `Uint8Array` -- not a `Buffer`, not an array, not a string
 * @returns {Uint8Array} 32 bytes
 */
export function sha256(bytes) {
  if (!(bytes instanceof Uint8Array)) throw new TypeError('sha256: a Uint8Array is required');
  const padded = pad(bytes);
  const state = H.slice();
  const schedule = new Int32Array(64);
  for (let offset = 0; offset < padded.length; offset += BLOCK) {
    compress(state, padded, offset, schedule);
  }
  const out = new Uint8Array(32);
  for (let i = 0; i < 8; i++) {
    out[4 * i] = state[i] >>> 24;
    out[4 * i + 1] = (state[i] >>> 16) & 0xff;
    out[4 * i + 2] = (state[i] >>> 8) & 0xff;
    out[4 * i + 3] = state[i] & 0xff;
  }
  return out;
}

/**
 * HMAC-SHA-256, RFC 2104.
 *
 * The key is bytes and not text: the per-pairing key of D171 is whatever the
 * pairing wrote into the secure store, and this module has no opinion about
 * what that is. A key longer than the block is hashed first, which is the
 * construction's only branch.
 *
 * @param {unknown} keyBytes a `Uint8Array`
 * @param {unknown} messageBytes a `Uint8Array`
 * @returns {Uint8Array} 32 bytes
 */
export function hmacSha256(keyBytes, messageBytes) {
  if (!(keyBytes instanceof Uint8Array)) throw new TypeError('hmacSha256: a Uint8Array key is required');
  if (!(messageBytes instanceof Uint8Array)) {
    throw new TypeError('hmacSha256: a Uint8Array message is required');
  }
  const key = keyBytes.length > BLOCK ? sha256(keyBytes) : keyBytes;
  const inner = new Uint8Array(BLOCK + messageBytes.length);
  const outer = new Uint8Array(BLOCK + 32);
  for (let i = 0; i < BLOCK; i++) {
    // The key is zero-padded to the block; past its length the pad bytes are
    // XORed with zero, which is the pad byte itself.
    const byte = i < key.length ? key[i] : 0;
    inner[i] = byte ^ 0x36;
    outer[i] = byte ^ 0x5c;
  }
  inner.set(messageBytes, BLOCK);
  outer.set(sha256(inner), BLOCK);
  return sha256(outer);
}

/**
 * A byte string as lowercase hex -- the spelling §3.5 fixes for a tag, and the
 * only spelling that ever reaches a database key, a log line or a report.
 *
 * @param {unknown} bytes a `Uint8Array`
 * @returns {string} two characters per byte
 */
export function toHex(bytes) {
  if (!(bytes instanceof Uint8Array)) throw new TypeError('toHex: a Uint8Array is required');
  let out = '';
  for (let i = 0; i < bytes.length; i++) {
    out += HEX[bytes[i] >>> 4] + HEX[bytes[i] & 0x0f];
  }
  return out;
}

// pad applies the standard's padding: a 0x80 byte, zeros to the end of the
// block that holds the length, and the message's bit length as a 64-bit
// big-endian integer. The result is a whole number of blocks, so the caller
// has one loop and no tail case.
//
// The bit length is computed with multiplication rather than `<< 3`: JavaScript's
// shift operators are 32-bit, and a message of 512 MB or more would silently
// wrap. `length * 8` is exact in a double for every length a message can have
// here, and `>>> 0` takes its low 32 bits the way the standard's little-endian
// arithmetic would; `<< 3` would not.
function pad(bytes) {
  const paddedLength = (Math.floor((bytes.length + 8) / BLOCK) + 1) * BLOCK;
  const padded = new Uint8Array(paddedLength);
  padded.set(bytes);
  padded[bytes.length] = 0x80;
  const bits = bytes.length * 8;
  const view = new DataView(padded.buffer);
  view.setUint32(paddedLength - 8, Math.floor(bits / 0x1_0000_0000));
  view.setUint32(paddedLength - 4, bits >>> 0);
  return padded;
}

// compress is the standard's block function, over one 64-byte block: the
// schedule, then the 64 rounds. `state` is updated in place.
function compress(state, block, offset, w) {
  for (let i = 0; i < 16; i++) {
    const at = offset + 4 * i;
    w[i] = (block[at] << 24) | (block[at + 1] << 16) | (block[at + 2] << 8) | block[at + 3];
  }
  for (let i = 16; i < 64; i++) {
    const x = w[i - 15];
    const y = w[i - 2];
    const s0 = ((x >>> 7) | (x << 25)) ^ ((x >>> 18) | (x << 14)) ^ (x >>> 3);
    const s1 = ((y >>> 17) | (y << 15)) ^ ((y >>> 19) | (y << 13)) ^ (y >>> 10);
    w[i] = (w[i - 16] + s0 + w[i - 7] + s1) | 0;
  }

  let a = state[0];
  let b = state[1];
  let c = state[2];
  let d = state[3];
  let e = state[4];
  let f = state[5];
  let g = state[6];
  let h = state[7];

  for (let i = 0; i < 64; i++) {
    const S1 = ((e >>> 6) | (e << 26)) ^ ((e >>> 11) | (e << 21)) ^ ((e >>> 25) | (e << 7));
    const ch = (e & f) ^ (~e & g);
    const temp1 = (h + S1 + ch + K[i] + w[i]) | 0;
    const S0 = ((a >>> 2) | (a << 30)) ^ ((a >>> 13) | (a << 19)) ^ ((a >>> 22) | (a << 10));
    const maj = (a & b) ^ (a & c) ^ (b & c);
    const temp2 = (S0 + maj) | 0;

    h = g;
    g = f;
    f = e;
    e = (d + temp1) | 0;
    d = c;
    c = b;
    b = a;
    a = (temp1 + temp2) | 0;
  }

  state[0] = (state[0] + a) | 0;
  state[1] = (state[1] + b) | 0;
  state[2] = (state[2] + c) | 0;
  state[3] = (state[3] + d) | 0;
  state[4] = (state[4] + e) | 0;
  state[5] = (state[5] + f) | 0;
  state[6] = (state[6] + g) | 0;
  state[7] = (state[7] + h) | 0;
}
