// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// AES-256, the forward direction only (mobile plan, Task P6.8; FIPS 197).
//
// **Why this is written here.** The delivery envelope is AES-256-GCM (§8), and
// this app has to open one on a phone. Metro resolves no `node:` builtin --
// `src/mailbox/sha256.js` exists for exactly that reason -- and the app's
// dependency list is the owner's decision, so `@noble/ciphers` is not a line
// this task may add. What it does instead is the tree's own answer: write the
// primitive, and hold it to vectors.
//
// **The forward direction only, and that is not an omission.** GCM encrypts:
// the tag and the keystream are both AES applied forward (`internal/budget/
// token`'s `cipher.NewGCM` does the same), so the inverse cipher and the
// inverse key schedule are code nothing could call. A decryption table that
// nothing uses is a table a reviewer has to read and check for no reason.
//
// **How it is held to its vectors.** `aes.test.js` runs FIPS 197's AES-256
// vector (appendix C.3), and `aes-gcm.test.js` runs NIST's GCM vectors and
// cross-checks every case against `node:crypto`'s OpenSSL. The end-to-end
// proof is `envelope.test.js`: it opens an envelope the **Go command sealed**,
// so these tables are checked against the same primitive `crypto/aes` uses and
// not only against a table of my own.

/**
 * The substitution box (FIPS 197 figure 7), as the bytes it is: the first row
 * is `0x63, 0x7c, 0x77, …` and every row is sixteen entries. It is written
 * once here rather than derived from the field's inverse because a derivation
 * is more code to check than the table it produces, and the table is what the
 * standard prints.
 */
const SBOX = Uint8Array.from(
  ('63 7c 77 7b f2 6b 6f c5 30 01 67 2b fe d7 ab 76 ' +
    'ca 82 c9 7d fa 59 47 f0 ad d4 a2 af 9c a4 72 c0 ' +
    'b7 fd 93 26 36 3f f7 cc 34 a5 e5 f1 71 d8 31 15 ' +
    '04 c7 23 c3 18 96 05 9a 07 12 80 e2 eb 27 b2 75 ' +
    '09 83 2c 1a 1b 6e 5a a0 52 3b d6 b3 29 e3 2f 84 ' +
    '53 d1 00 ed 20 fc b1 5b 6a cb be 39 4a 4c 58 cf ' +
    'd0 ef aa fb 43 4d 33 85 45 f9 02 7f 50 3c 9f a8 ' +
    '51 a3 40 8f 92 9d 38 f5 bc b6 da 21 10 ff f3 d2 ' +
    'cd 0c 13 ec 5f 97 44 17 c4 a7 7e 3d 64 5d 19 73 ' +
    '60 81 4f dc 22 2a 90 88 46 ee b8 14 de 5e 0b db ' +
    'e0 32 3a 0a 49 06 24 5c c2 d3 ac 62 91 95 e4 79 ' +
    'e7 c8 37 6d 8d d5 4e a9 6c 56 f4 ea 65 7a ae 08 ' +
    'ba 78 25 2e 1c a6 b4 c6 e8 dd 74 1f 4b bd 8b 8a ' +
    '70 3e b5 66 48 03 f6 0e 61 35 57 b9 86 c1 1d 9e ' +
    'e1 f8 98 11 69 d9 8e 94 9b 1e 87 e9 ce 55 28 df ' +
    '8c a1 89 0d bf e6 42 68 41 99 2d 0f b0 54 bb 16').split(' '),
  (hex) => parseInt(hex, 16),
);

/** AES-256's round constants: one byte each, for the ten rounds the key
 * schedule's 256-bit form needs (§5.2, the `Rcon` column). */
const RCON = Uint8Array.from([0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40]);

/** AES-256's shape: an eight-word key, a fourteen-round cipher, a 240-byte
 * expanded key. */
const NK = 8;
const NR = 14;
const KEY_WORDS = 4 * (NR + 1);

/** AES's block: 16 bytes, in and out. */
export const AES_BLOCK = 16;

/** AES-256's key: 32 bytes. */
export const AES_KEY_SIZE = 32;

/** Whether a value is a 32-byte key. The only key this module takes. */
export function isAesKey(key) {
  return key instanceof Uint8Array && key.length === AES_KEY_SIZE;
}

/** GF(2^8)'s doubling, the `xtime` of §4.2.1: shift left and reduce by the
 * field's polynomial when the top bit falls off. */
function xtime(byte) {
  const shifted = byte << 1;
  return (shifted ^ (shifted & 0x100 ? 0x1b : 0)) & 0xff;
}

/** `SubWord`: the S-box applied to a word's four bytes. */
function subWord(word) {
  return (
    (SBOX[(word >>> 24) & 0xff] << 24) ^
    (SBOX[(word >>> 16) & 0xff] << 16) ^
    (SBOX[(word >>> 8) & 0xff] << 8) ^
    SBOX[word & 0xff]
  );
}

/** `RotWord`: a word's bytes rotated left by one. */
function rotWord(word) {
  return ((word << 8) | (word >>> 24)) >>> 0;
}

/**
 * The key schedule (§5.2): `KEY_WORDS` 32-bit words, each round's key four of
 * them. It is per envelope -- one call per open -- because a cached schedule
 * would be a key held alive past the call that used it.
 */
function expandKey(key) {
  const w = new Uint32Array(KEY_WORDS);
  for (let i = 0; i < NK; i += 1) {
    w[i] = ((key[4 * i] << 24) | (key[4 * i + 1] << 16) | (key[4 * i + 2] << 8) | key[4 * i + 3]) >>> 0;
  }
  for (let i = NK; i < KEY_WORDS; i += 1) {
    let temp = w[i - 1];
    if (i % NK === 0) {
      temp = (subWord(rotWord(temp)) ^ (RCON[i / NK - 1] << 24)) >>> 0;
    } else if (i % NK === 4) {
      temp = subWord(temp);
    }
    w[i] = (w[i - NK] ^ temp) >>> 0;
  }
  return w;
}

/** `AddRoundKey`: the state XORed with one round's four words. */
function addRoundKey(state, w, round) {
  for (let column = 0; column < 4; column += 1) {
    const word = w[4 * round + column];
    state[4 * column] ^= (word >>> 24) & 0xff;
    state[4 * column + 1] ^= (word >>> 16) & 0xff;
    state[4 * column + 2] ^= (word >>> 8) & 0xff;
    state[4 * column + 3] ^= word & 0xff;
  }
}

/** `SubBytes`, in place. */
function subBytes(state) {
  for (let i = 0; i < AES_BLOCK; i += 1) state[i] = SBOX[state[i]];
}

/** `ShiftRows`: row `r` rotated left by `r`, with the state read column-major
 * (`state[4 * column + row]`, §3.4). */
function shiftRows(state) {
  let held = state[1];
  state[1] = state[5];
  state[5] = state[9];
  state[9] = state[13];
  state[13] = held;

  held = state[2];
  state[2] = state[10];
  state[10] = held;
  held = state[6];
  state[6] = state[14];
  state[14] = held;

  held = state[15];
  state[15] = state[11];
  state[11] = state[7];
  state[7] = state[3];
  state[3] = held;
}

/** `MixColumns`, in place: each column multiplied by the fixed polynomial. */
function mixColumns(state) {
  for (let column = 0; column < 4; column += 1) {
    const at = 4 * column;
    const a0 = state[at];
    const a1 = state[at + 1];
    const a2 = state[at + 2];
    const a3 = state[at + 3];
    const all = a0 ^ a1 ^ a2 ^ a3;
    state[at] ^= all ^ xtime(a0 ^ a1);
    state[at + 1] ^= all ^ xtime(a1 ^ a2);
    state[at + 2] ^= all ^ xtime(a2 ^ a3);
    state[at + 3] ^= all ^ xtime(a3 ^ a0);
  }
}

/**
 * Encrypt one 16-byte block in place: the ten rounds of §5.1, with the initial
 * `AddRoundKey`, the `MixColumns`-less last round, and no inverse anywhere.
 *
 * `key` must be a schedule from `expandKey`. It is the caller's -- the GCM
 * mode builds it once and encrypts every block of the payload with it -- and
 * it is never returned by anything.
 */
export function encryptBlock(schedule, block) {
  const state = new Uint8Array(AES_BLOCK);
  state.set(block);
  addRoundKey(state, schedule, 0);
  for (let round = 1; round < NR; round += 1) {
    subBytes(state);
    shiftRows(state);
    mixColumns(state);
    addRoundKey(state, schedule, round);
  }
  subBytes(state);
  shiftRows(state);
  addRoundKey(state, schedule, NR);
  block.set(state);
  return block;
}

/** The schedule for a 32-byte key, for the GCM mode below: it builds one and
 * then encrypts every block of the payload with it. */
export function keySchedule(key) {
  if (!isAesKey(key)) throw new TypeError('keySchedule takes the 32 bytes of an AES-256 key');
  return expandKey(key);
}
