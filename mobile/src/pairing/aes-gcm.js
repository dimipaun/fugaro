// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// AES-256-GCM, the opening direction (mobile plan, Task P6.8; NIST SP 800-38D).
//
// `internal/budget/token`'s delivery envelope is `cipher.NewGCM` over
// AES-256, and this is the phone's half of it: authenticate, then decrypt.
//
// **Why it is written here** is `aes.js`'s reason, and so is the direction:
// GCM's tag and keystream are both the forward cipher, so nothing in this file
// needs a decryption table. The one operation GCM adds is GHASH, which is
// multiplication in GF(2^128).
//
// **The bit order is the part that is easy to get wrong, and it is stated
// once.** GCM numbers the bits of a block from the *most* significant bit of
// byte 0 (SP 800-38D §6.1, "the i-th bit of the string"), so the field's
// multiplication reads bit 0 as the top bit of the first byte, and the
// reduction constant `0xe1` is applied to the first byte after a right shift.
// A little-endian reading of the same bytes is the *other* convention, would
// produce a self-consistent cipher, and would fail every vector in the world
// -- which is why `aes-gcm.test.js` runs NIST's own vectors and cross-checks
// against OpenSSL, and why `envelope.test.js` opens what `crypto/aes` sealed.
//
// **What an open answers.** Either the plaintext, or a refusal: the tag
// mismatch and the length refusal are the only two, and there is no third
// state. Nothing is decrypted before the tag is verified -- SP 800-38D's
// requirement, and the reason the comparison below happens on the whole tag
// before a single plaintext byte exists.

import { AES_BLOCK, encryptBlock, keySchedule } from './aes.js';

/** GCM's tag: 128 bits, the only size this protocol uses (§8's layout). */
export const GCM_TAG_SIZE = 16;

/** GCM's nonce here: 96 bits, the size SP 800-38D §5.2.1.1 makes the fast
 * path -- `internal/budget/token`'s `nonceSize`. */
export const GCM_NONCE_SIZE = 12;

/** How this module refuses. */
export const GCM_CODES = Object.freeze({
  BAD_NONCE: 'bad-nonce',
  BAD_TAG: 'bad-tag',
  /** The tag did not verify: the ciphertext, the AAD or the key is not the one
   * the sender used. One code for all three, deliberately -- telling an
   * attacker which of them was wrong is telling them what to change. */
  AUTHENTICATION_FAILED: 'authentication-failed',
});

function refuse(code) {
  return Object.freeze({ ok: false, code });
}

/** XOR `source` into `target`, both 16 bytes. */
function xorInto(target, source) {
  for (let i = 0; i < AES_BLOCK; i += 1) target[i] ^= source[i];
}

/**
 * Multiplication in GF(2^128), SP 800-38D §6.3's algorithm: the
 * bit-by-bit `Z ← Z ⊕ V`, `V ← V·x` loop, with the reduction by
 * `x^128 + x^7 + x^2 + x + 1` written as the `0xe1` byte.
 *
 * It is the readable form and not the table-driven one: an envelope's payload
 * is a few hundred bytes, so a handful of thousand iterations, and a reviewer
 * can check this loop against the standard's pseudocode line by line.
 */
function ghashMultiply(x, y) {
  const z = new Uint8Array(AES_BLOCK);
  const v = Uint8Array.from(y);
  for (let bit = 0; bit < 128; bit += 1) {
    if ((x[bit >> 3] >> (7 - (bit & 7))) & 1) xorInto(z, v);
    const lost = v[AES_BLOCK - 1] & 1;
    for (let i = AES_BLOCK - 1; i > 0; i -= 1) {
      v[i] = ((v[i] >>> 1) | ((v[i - 1] & 1) << 7)) & 0xff;
    }
    v[0] = (v[0] >>> 1) ^ (lost ? 0xe1 : 0);
  }
  return z;
}

/** GHASH over a list of 16-byte blocks, with `h` the hash subkey. */
function ghash(h, blocks) {
  const y = new Uint8Array(AES_BLOCK);
  for (const block of blocks) {
    xorInto(y, block);
    y.set(ghashMultiply(y, h));
  }
  return y;
}

/** `s` padded to a whole number of blocks with zeros, as its own array of
 * blocks. The last block is a copy, so the caller's bytes are never written
 * to. */
function toBlocks(bytes) {
  const blocks = [];
  for (let at = 0; at < bytes.length; at += AES_BLOCK) {
    const block = new Uint8Array(AES_BLOCK);
    block.set(bytes.subarray(at, at + AES_BLOCK));
    blocks.push(block);
  }
  return blocks;
}

/** Two 64-bit lengths, big-endian, as SP 800-38D §6.4's last block: the AAD's
 * bit length and the ciphertext's. */
function lengthBlock(aadBytes, ciphertextBytes) {
  const block = new Uint8Array(AES_BLOCK);
  const aadBits = aadBytes * 8;
  const textBits = ciphertextBytes * 8;
  // The high words are written as zero because neither length can reach 2^32
  // bits here (that is 512 MiB of AAD, and the envelope is bounded well below
  // it); they are written rather than skipped because the layout is the
  // standard's and a reader should see all four words.
  const view = new DataView(block.buffer);
  view.setUint32(0, Math.floor(aadBits / 2 ** 32));
  view.setUint32(4, aadBits >>> 0);
  view.setUint32(8, Math.floor(textBits / 2 ** 32));
  view.setUint32(12, textBits >>> 0);
  return block;
}

/**
 * The counter block for `counter`: the nonce, then the 32-bit big-endian
 * counter with the top bit forced clear (§6.2's `inc32`, and the encoding J0
 * uses). GCM starts at `inc32(J0)` for the data, which is why the caller
 * passes 2 for the first data block (J0 itself is 1).
 */
function counterBlock(nonce, counter) {
  const block = new Uint8Array(AES_BLOCK);
  block.set(nonce, 0);
  new DataView(block.buffer).setUint32(GCM_NONCE_SIZE, counter >>> 0);
  return block;
}

/** The tag of §7.1: `GCTR` of J0 over GHASH, which is the hash subkey's own
 * encryption masked by the GHASH of the message. */
function authenticationTag(schedule, h, j0, aad, ciphertext) {
  const blocks = [...toBlocks(aad), ...toBlocks(ciphertext), lengthBlock(aad.length, ciphertext.length)];
  const hash = ghash(h, blocks);
  const mask = encryptBlock(schedule, Uint8Array.from(j0));
  return Uint8Array.from(hash, (byte, i) => byte ^ mask[i]);
}

/** Compare two tags without an early exit, so how far a guess got is not
 * something a caller can measure. */
function tagsEqual(a, b) {
  let difference = 0;
  for (let i = 0; i < GCM_TAG_SIZE; i += 1) difference |= a[i] ^ b[i];
  return difference === 0;
}

/**
 * Open one GCM ciphertext: verify the tag, then decrypt.
 *
 * `aad` is the additional authenticated data -- for the delivery envelope,
 * §8's `deliveryAAD` -- and it is *authenticated and never encrypted*: it
 * never appears in the plaintext, and changing one byte of it makes the tag
 * fail. That is the whole of "the AAD names this uid and this key" in §7.2:
 * the check is not a comparison this module makes, it is the tag's.
 *
 * The plaintext is returned only after the tags are equal, so a caller can
 * never hold unauthenticated bytes.
 */
export function gcmOpen({ key, nonce, aad, ciphertext, tag }) {
  if (!(nonce instanceof Uint8Array) || nonce.length !== GCM_NONCE_SIZE) return refuse(GCM_CODES.BAD_NONCE);
  if (!(tag instanceof Uint8Array) || tag.length !== GCM_TAG_SIZE) return refuse(GCM_CODES.BAD_TAG);
  if (!(aad instanceof Uint8Array) || !(ciphertext instanceof Uint8Array)) return refuse(GCM_CODES.BAD_TAG);

  const schedule = keySchedule(key);
  // H is the cipher applied to the zero block, and J0 is the nonce with the
  // counter 1 -- both the 96-bit-nonce path of §5.2.1.1, which is the only
  // nonce size this protocol holds.
  const h = encryptBlock(schedule, new Uint8Array(AES_BLOCK));
  const j0 = counterBlock(nonce, 1);
  const expected = authenticationTag(schedule, h, j0, aad, ciphertext);
  if (!tagsEqual(expected, tag)) return refuse(GCM_CODES.AUTHENTICATION_FAILED);

  const plaintext = new Uint8Array(ciphertext.length);
  for (let at = 0; at < ciphertext.length; at += AES_BLOCK) {
    const keystream = encryptBlock(schedule, counterBlock(nonce, 2 + at / AES_BLOCK));
    const end = Math.min(at + AES_BLOCK, ciphertext.length);
    for (let i = at; i < end; i += 1) plaintext[i] = ciphertext[i] ^ keystream[i - at];
  }
  return Object.freeze({ ok: true, plaintext });
}
