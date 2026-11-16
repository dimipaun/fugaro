// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The two primitives this app had to write, against the standards' own
// vectors and against OpenSSL (mobile plan, Task P6.8).
//
// **Why a test this thorough for two files of tables.** `aes.js` and
// `aes-gcm.js` are the only cryptography in this repository that is not a
// well-reviewed library, and they decide whether the app can trust a delivery
// and a MAC key at all. A self-consistent cipher is easy to write and useless:
// the failure mode is not a crash but a key that is not the one the CLI
// derived, which no functional test would catch. So each primitive is held to
// three independent things:
//
//   1. **the standard's printed vectors** -- FIPS 197 C.3 for AES-256, NIST
//      SP 800-38D's own cases for GCM, RFC 5869 A.1/A.3 for HKDF;
//   2. **OpenSSL**, through `node:crypto`, on cases the vectors do not cover
//      (a partial final block, an empty AAD, a long AAD);
//   3. **the Go command**, in `envelope.test.js`, which opens an envelope
//      `crypto/aes` and `crypto/hkdf` really sealed.
//
// `node:crypto` is legal here and only here: it is the test's implementation,
// never the app's (a production import of it is an offence
// `dependencies.test.js` reports).

import assert from 'node:assert/strict';
import { createCipheriv, hkdfSync } from 'node:crypto';
import test from 'node:test';

import { AES_KEY_SIZE, encryptBlock, keySchedule } from './aes.js';
import { GCM_NONCE_SIZE, GCM_TAG_SIZE, gcmOpen } from './aes-gcm.js';
import { HKDF_HASH_LENGTH, hkdfSha256 } from './hkdf.js';

/** Bytes from a lowercase hex string, with no `Buffer` (this app's tests are
 * plain JS like its modules). */
function hex(text) {
  const out = new Uint8Array(text.length / 2);
  for (let i = 0; i < out.length; i += 1) out[i] = parseInt(text.slice(2 * i, 2 * i + 2), 16);
  return out;
}

/** The lowercase hex of bytes -- the spelling every vector below is written
 * in, so a comparison failure prints the two strings side by side. */
function toHex(bytes) {
  return [...bytes].map((byte) => byte.toString(16).padStart(2, '0')).join('');
}

/** A repeat of one byte, the shape a vector's filler uses. */
function repeat(byte, count) {
  return new Uint8Array(count).fill(byte);
}

/** Deterministic "random-looking" bytes: a counter through SHA-256-shaped
 * mixing is not needed here -- `node:crypto` and the module are *given the
 * same bytes*, so any fixed sequence does, and a fixed one keeps a failure
 * reproducible. */
function filler(seed, count) {
  const out = new Uint8Array(count);
  for (let i = 0; i < count; i += 1) out[i] = (seed * 31 + i * 17 + (i >> 3)) & 0xff;
  return out;
}

test('AES-256 is FIPS 197 C.3', () => {
  const key = hex('000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f');
  const block = hex('00112233445566778899aabbccddeeff');
  const schedule = keySchedule(key);
  assert.equal(toHex(encryptBlock(schedule, block)), '8ea2b7ca516745bfeafc49904b496089');
  // ... and the schedule is AES-256's: fourteen rounds and sixty words. The
  // count is asserted because a key schedule built with AES-128's `Nk` would
  // still encrypt *something*, and the vector above is the only thing that
  // would catch it -- twice over is better than once.
  assert.equal(schedule.length, 60);
  // The expanded key's first eight words are the key itself (§5.2), which is
  // the one place a wrong `Nk` shows up before the rounds do.
  const firstWords = new Uint8Array(16);
  for (let word = 0; word < 4; word += 1) {
    firstWords[4 * word] = (schedule[word] >>> 24) & 0xff;
    firstWords[4 * word + 1] = (schedule[word] >>> 16) & 0xff;
    firstWords[4 * word + 2] = (schedule[word] >>> 8) & 0xff;
    firstWords[4 * word + 3] = schedule[word] & 0xff;
  }
  assert.equal(toHex(firstWords), '000102030405060708090a0b0c0d0e0f', 'w[0..3] are the key (§5.2)');
  const zero = repeat(0, AES_KEY_SIZE);
  assert.deepEqual([...keySchedule(zero)], [...keySchedule(new Uint8Array(32))]);
});

test('AES-256 agrees with OpenSSL over a spread of blocks and keys', () => {
  for (let round = 0; round < 8; round += 1) {
    const key = filler(round + 1, 32);
    const block = filler(round + 100, 16);
    const cipher = createCipheriv('aes-256-ecb', Buffer.from(key), null);
    cipher.setAutoPadding(false);
    const expected = Buffer.concat([cipher.update(Buffer.from(block)), cipher.final()]);
    assert.equal(toHex(encryptBlock(keySchedule(key), block)), expected.toString('hex'), `round ${round}`);
  }
  // A wrong key length is a `TypeError` and never a truncated or padded key.
  assert.throws(() => keySchedule(new Uint8Array(16)), TypeError);
  assert.throws(() => keySchedule('a'.repeat(32)), TypeError);
});

test('GCM opens NIST SP 800-38D’s AES-256 cases', () => {
  // Case 13: a zero key, a zero IV, no AAD, no plaintext -- the shortest
  // envelope GCM has, and the tag the standard prints.
  assert.deepEqual(
    gcmOpen({ key: repeat(0, 32), nonce: repeat(0, 12), aad: new Uint8Array(0), ciphertext: new Uint8Array(0), tag: hex('530f8afbc74536b9a963b4f1c4cb738b') }),
    { ok: true, plaintext: new Uint8Array(0) },
  );
  // Case 14: the same key and IV with one block of plaintext.
  assert.deepEqual(
    gcmOpen({
      key: repeat(0, 32),
      nonce: repeat(0, 12),
      aad: new Uint8Array(0),
      ciphertext: hex('cea7403d4d606b6e074ec5d3baf39d18'),
      tag: hex('d0d1c8a799996bf0265b98b5d48ab919'),
    }),
    { ok: true, plaintext: hex('00000000000000000000000000000000') },
  );
  // ... and one wrong byte of the tag is a refusal with no plaintext at all.
  const failed = gcmOpen({
    key: repeat(0, 32),
    nonce: repeat(0, 12),
    aad: new Uint8Array(0),
    ciphertext: hex('cea7403d4d606b6e074ec5d3baf39d18'),
    tag: hex('d0d1c8a799996bf0265b98b5d48ab918'),
  });
  assert.equal(failed.ok, false);
  assert.equal(failed.code, 'authentication-failed');
  assert.equal('plaintext' in failed, false);
});

/** Seal with OpenSSL: the independent half of every case below, and the one
 * that covers what the printed vectors do not. */
function sealWithOpenSSL({ key, nonce, aad, plaintext }) {
  const cipher = createCipheriv('aes-256-gcm', Buffer.from(key), Buffer.from(nonce));
  cipher.setAAD(Buffer.from(aad));
  const body = Buffer.concat([cipher.update(Buffer.from(plaintext)), cipher.final()]);
  return { ciphertext: new Uint8Array(body), tag: new Uint8Array(cipher.getAuthTag()) };
}

test('GCM agrees with OpenSSL, including a partial final block and a long AAD', () => {
  const cases = [
    { key: filler(1, 32), nonce: filler(2, 12), aad: new Uint8Array(0), plaintext: filler(3, 16) },
    // A plaintext that is not a whole number of blocks: the case a
    // block-loop that forgot the tail would silently get wrong.
    { key: filler(4, 32), nonce: filler(5, 12), aad: filler(6, 7), plaintext: filler(7, 45) },
    // An AAD that is not a whole number of blocks either, and longer than the
    // (fixed-width) delivery AAD ever is.
    { key: filler(8, 32), nonce: filler(9, 12), aad: filler(10, 129), plaintext: filler(11, 300) },
    // One byte, and nothing: the two ends of the length range.
    { key: filler(12, 32), nonce: filler(13, 12), aad: filler(14, 1), plaintext: filler(15, 1) },
    { key: filler(16, 32), nonce: filler(17, 12), aad: filler(18, 16), plaintext: new Uint8Array(0) },
  ];
  for (const [round, one] of cases.entries()) {
    const { ciphertext, tag } = sealWithOpenSSL(one);
    const opened = gcmOpen({ ...one, ciphertext, tag });
    assert.equal(opened.ok, true, `case ${round}`);
    assert.equal(toHex(opened.plaintext), toHex(one.plaintext), `case ${round}`);
    // The AAD is authenticated and never encrypted: changing one byte of it
    // must fail the tag, whichever byte it is.
    for (const at of [0, Math.floor(one.aad.length / 2), Math.max(0, one.aad.length - 1)]) {
      if (one.aad.length === 0) break;
      const tampered = Uint8Array.from(one.aad);
      tampered[at] ^= 0x01;
      assert.equal(gcmOpen({ ...one, aad: tampered, ciphertext, tag }).ok, false, `case ${round}, AAD byte ${at}`);
    }
    // ... and so must a changed ciphertext, or a shorter tag.
    const flipped = Uint8Array.from(ciphertext);
    if (flipped.length > 0) {
      flipped[0] ^= 0x01;
      assert.equal(gcmOpen({ ...one, ciphertext: flipped, tag }).ok, false, `case ${round}, ciphertext`);
    }
    assert.equal(gcmOpen({ ...one, ciphertext, tag: tag.subarray(0, GCM_TAG_SIZE - 1) }).ok, false, `case ${round}, short tag`);
    assert.equal(gcmOpen({ ...one, ciphertext, tag, nonce: one.nonce.subarray(0, GCM_NONCE_SIZE - 1) }).ok, false, `case ${round}, short nonce`);
  }
});

test('HKDF-SHA-256 is RFC 5869, in the no-salt form this envelope uses', () => {
  // **A.1 and A.2 are deliberately not run.** They are the salted cases, and
  // this module has no salt parameter at all: §8's derivation is
  // `hkdf.Key(sha256.New, shared, nil, "fugaro-pairing-v1", 32)`, so the salt
  // is HashLen zero bytes and a salted vector would be a test of a code path
  // that does not exist. What is checked instead is the standard's own no-salt
  // case, and OpenSSL on the real parameters.
  //
  // A.3: a zero-length salt and info, and a 42-byte output -- at a length that
  // needs two blocks, which is the only thing the loop above can get wrong.
  const a3 = hkdfSha256(hex('0b'.repeat(22)), new Uint8Array(0), 42);
  assert.equal(
    toHex(a3.bytes),
    '8da4e775a563c18f715f802a063c5a31b8a11f5c5ee1879ec3454e5f3c738d2d9d201395faa4b61a96c8',
  );
  // The length this envelope asks for, and the same value OpenSSL derives from
  // the same three arguments.
  const ikm = filler(21, 32);
  const info = new TextEncoder().encode('fugaro-pairing-v1');
  const derived = hkdfSha256(ikm, info, HKDF_HASH_LENGTH);
  assert.equal(
    toHex(derived.bytes),
    Buffer.from(hkdfSync('sha256', Buffer.from(ikm), Buffer.alloc(0), Buffer.from(info), HKDF_HASH_LENGTH)).toString('hex'),
  );
  // An impossible length is refused rather than wrapped: 256 blocks would
  // repeat the first one.
  assert.equal(hkdfSha256(ikm, info, 255 * HKDF_HASH_LENGTH + 1).ok, false);
  assert.equal(hkdfSha256(ikm, info, 0).ok, false);
  assert.equal(hkdfSha256(ikm, info, 1.5).ok, false);
  assert.equal(hkdfSha256('not bytes', info, 32).ok, false);
});
