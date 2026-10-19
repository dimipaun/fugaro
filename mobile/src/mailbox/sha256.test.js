// SHA-256 and HMAC-SHA-256, written out in the app (mobile plan, Task P3.6;
// functional-spec-reconciliation.md §3.5, D171).
//
// **Why the app cannot use Node's crypto, and why this file is therefore a
// test of arithmetic.** P6.3 made `mac` required on every inbox message and
// put the tag's node implementation in `shared/mailbox-schema/mac.js` -- which
// imports `node:crypto` and is deliberately never imported by that package's
// entry point, because Metro (the bundler the app is built with) resolves no
// `node:` builtin and Hermes (the engine) has no `Buffer`. The app still has
// to verify the tags the consumer writes into its outbox, so it computes
// HMAC-SHA-256 itself -- `sha256.js`, a few hundred lines of 32-bit integer
// arithmetic and nothing else -- and this file is what holds it to the
// protocol.
//
// **Nothing here is trusted to agree with itself.** Every vector below is
// either a published one (FIPS 180-4 for SHA-256, RFC 4231 for HMAC) or
// computed by **Node's own crypto**, which is a second implementation rather
// than a second copy: a defect in the hand-rolled arithmetic fails these
// tests instead of matching itself. The differential case is generated from a
// seeded integer generator written in this file, so a failing run prints the
// same inputs the next time.
//
// **The one place the two implementations must agree byte for byte** is
// `vectors.json` -- the cross-language contract P6.3 wrote and the Go consumer
// reimplements -- and that check is `mac.test.js`'s, over the module that uses
// this one. This file proves the primitive; that one proves the protocol.

import assert from 'node:assert/strict';
import { createHash, createHmac } from 'node:crypto';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import { hmacSha256, sha256, toHex, utf8Bytes } from './sha256.js';

const HERE = import.meta.dirname;

/** Node's own SHA-256 of some bytes, as lowercase hex: the reference. */
const reference = (bytes) => createHash('sha256').update(Buffer.from(bytes)).digest('hex');

/** Node's own HMAC-SHA-256 of some bytes under some key: the reference. */
const referenceHmac = (key, bytes) =>
  createHmac('sha256', Buffer.from(key)).update(Buffer.from(bytes)).digest('hex');

/** The digest of a UTF-8 string, hex, through this module. */
const digestOf = (text) => toHex(sha256(utf8Bytes(text)));

// --- the published vectors -------------------------------------------------

test('FIPS 180-4\'s SHA-256 vectors', () => {
  // The two the standard states in its own examples ...
  assert.equal(
    digestOf(''),
    'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
    'the empty message: one padding block, no data',
  );
  assert.equal(
    digestOf('abc'),
    'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad',
  );
  // ... and the 56-byte one, which is the length that forces the padding into
  // a second block (56 bytes of data + 0x80 + 8 length bytes = 65 > 64).
  assert.equal(
    digestOf('abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq'),
    '248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1',
  );
});

test('the millionth "a": the long-message vector, computed in the app', () => {
  // The standard's third vector. It is slow enough to be worth naming and
  // fast enough to keep: it is the only case here that walks thousands of
  // blocks, so an off-by-one in the block loop shows up here and nowhere else.
  const bytes = new Uint8Array(1_000_000).fill(0x61);
  assert.equal(
    toHex(sha256(bytes)),
    'cdc76e5c9914fb9281a1c7e284d73e67f1809a48a497200e046d39ccc7112cd0',
  );
});

test('the padding boundaries, where a hash is most often wrong', () => {
  // 55 is the last length that fits its length field in the same block; 56 is
  // the first that does not; 64 is exactly one block, and 65 spills one byte
  // into the next. Each is checked against Node rather than against a stored
  // digest, so the test states the rule and not four more numbers.
  for (const length of [0, 1, 54, 55, 56, 57, 63, 64, 65, 119, 120, 127, 128, 129]) {
    const bytes = new Uint8Array(length);
    for (let i = 0; i < length; i++) bytes[i] = i & 0xff;
    assert.equal(toHex(sha256(bytes)), reference(bytes), `${length} bytes`);
  }
});

test('a differential run against Node\'s crypto, over generated inputs', () => {
  // A seeded generator rather than `Math.random`: a failure prints a case the
  // next run repeats. The lengths straddle every block boundary twice over and
  // include the module's own worst case, a message of no bytes at all.
  let seed = 0x5eed;
  const next = () => {
    seed = (seed * 1_103_515_245 + 12_345) & 0x7fffffff;
    return seed;
  };
  for (let i = 0; i < 200; i++) {
    const length = next() % 260;
    const bytes = new Uint8Array(length);
    for (let j = 0; j < length; j++) bytes[j] = next() & 0xff;
    assert.equal(toHex(sha256(bytes)), reference(bytes), `case ${i}, ${length} bytes`);
  }
});

test('the bytes are UTF-8, because the tag is over bytes', () => {
  // `canonicalBytes` returns a string whose UTF-8 encoding is what is signed,
  // so the encoder is part of the protocol. Each case is a shape JSON lets a
  // message carry and a naive `charCodeAt` implementation gets wrong.
  for (const text of [
    '',
    'abc',
    '€',                    // 3 bytes
    'ü',                    // 2 bytes
    '日本語',
    '🙂',                   // 4 bytes: a surrogate PAIR, which a
    '🙂🙂',                 // charCodeAt loop would encode as two 3-byte
    'a🙂b€c',               // sequences each and hash differently
    '{"text":"run 42 finished"}',
  ]) {
    assert.deepEqual(utf8Bytes(text), new Uint8Array(Buffer.from(text, 'utf8')), JSON.stringify(text));
    assert.equal(digestOf(text), reference(Buffer.from(text, 'utf8')), JSON.stringify(text));
  }
});

test('a value that is not a string, or not bytes, is refused rather than coerced', () => {
  // Fail closed: this module has no opinion about a number, a Buffer or a
  // string of hex, and computing *something* for one would sign bytes nobody
  // meant to sign.
  for (const notText of [undefined, null, 42, {}, ['a'], new Uint8Array([0x61])]) {
    assert.throws(() => utf8Bytes(notText), TypeError, String(notText));
  }
  for (const notBytes of [undefined, null, 'abc', [1, 2, 3], new Int8Array(3)]) {
    assert.throws(() => sha256(notBytes), TypeError, String(notBytes));
  }
  assert.throws(() => hmacSha256('key', utf8Bytes('x')), TypeError);
  assert.throws(() => hmacSha256(utf8Bytes('key'), 'x'), TypeError);
  assert.throws(() => toHex([1, 2, 3]), TypeError);
});

// --- HMAC ------------------------------------------------------------------

test('RFC 4231\'s HMAC-SHA-256 vectors, cases 1 to 7', () => {
  // The standard's own cases, key bytes included: the short key, the key that
  // is exactly one block, the 131-byte key that must be hashed first, and the
  // 50-byte messages that straddle nothing in particular -- the point of the
  // set is that it moves the key length, which is the one branch HMAC has.
  const cases = [
    // 1: 20 bytes of 0x0b
    [new Uint8Array(20).fill(0x0b), utf8Bytes('Hi There'),
      'b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7'],
    // 2: the Jefe case, the only one with an ASCII key
    [utf8Bytes('Jefe'), utf8Bytes('what do ya want for nothing?'),
      '5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843'],
    // 3: 20 bytes of 0xaa, 50 bytes of 0xdd
    [new Uint8Array(20).fill(0xaa), new Uint8Array(50).fill(0xdd),
      '773ea91e36800e46854db8ebd09181a72959098b3ef8c122d9635514ced565fe'],
    // 4: the counting key 0x01..0x19
    [Uint8Array.from({ length: 25 }, (_, i) => i + 1), new Uint8Array(50).fill(0xcd),
      '82558a389a443c0ea4cc819899f2083a85f0faa3e578f8077a2e3ff46729665b'],
    // 6: a 131-byte key, so the key is longer than the block and is hashed
    [new Uint8Array(131).fill(0xaa), utf8Bytes('Test Using Larger Than Block-Size Key - Hash Key First'),
      '60e431591ee0b67f0d8a26aacbf5b77f8e0bc6213728c5140546040f0ee37f54'],
    // 7: the same key, a long message too
    [new Uint8Array(131).fill(0xaa),
      utf8Bytes('This is a test using a larger than block-size key and a larger than block-size data. '
        + 'The key needs to be hashed before being used by the HMAC algorithm.'),
      '9b09ffa71b942fcb27635fbcd5b0e944bfdc63644f0713938a7f51535c3a35e2'],
  ];
  for (const [key, message, expected] of cases) {
    assert.equal(toHex(hmacSha256(key, message)), expected, expected.slice(0, 8));
    assert.equal(toHex(hmacSha256(key, message)), referenceHmac(key, message));
  }
});

test('the key-length branches, against Node', () => {
  // HMAC has one branch: a key of at most one block is padded, a longer one is
  // hashed first. 63, 64 and 65 are where that branch turns, and a key that is
  // *exactly* 64 bytes is the case an off-by-one gets wrong.
  const message = utf8Bytes('a message the phone signs');
  for (const length of [0, 1, 32, 63, 64, 65, 128, 131]) {
    const key = new Uint8Array(length);
    for (let i = 0; i < length; i++) key[i] = (i * 7 + 3) & 0xff;
    assert.equal(toHex(hmacSha256(key, message)), referenceHmac(key, message), `${length}-byte key`);
  }
});

test('HMAC over the empty message and the empty key', () => {
  // Both are legal inputs and both are the shape a caller that dropped a
  // field would produce; the answer is Node's, so the test says what HMAC
  // says rather than what this implementation happens to do.
  assert.equal(toHex(hmacSha256(new Uint8Array(0), new Uint8Array(0))),
    referenceHmac(new Uint8Array(0), new Uint8Array(0)));
  assert.equal(toHex(hmacSha256(new Uint8Array(0), utf8Bytes('m'))),
    referenceHmac(new Uint8Array(0), utf8Bytes('m')));
  assert.equal(toHex(hmacSha256(utf8Bytes('k'), new Uint8Array(0))),
    referenceHmac(utf8Bytes('k'), new Uint8Array(0)));
});

test('a message that is not a whole number of blocks is signed as it is', () => {
  // Nothing here pads the *message*: the tag covers the bytes the schema
  // produced, and one byte more or less is a different tag. A module that
  // padded to a block boundary before signing would still pass every vector
  // above (they are all short) and would fail on every real message.
  const key = utf8Bytes('not-a-secret-test-key-0001');
  for (const length of [1, 31, 32, 63, 64, 65, 127]) {
    const message = new Uint8Array(length).fill(0x2e);
    assert.equal(toHex(hmacSha256(key, message)), referenceHmac(key, message), `${length} bytes`);
  }
});

test('toHex is lowercase, fixed width, and refuses anything else', () => {
  assert.equal(toHex(new Uint8Array([0x00, 0x0f, 0xff])), '000fff');
  assert.equal(toHex(new Uint8Array(32)), '0'.repeat(64));
});

test('the primitive imports nothing at all, which is what makes it the phone\'s', () => {
  // The property P6.3's `README.md` states and this module exists for: Metro
  // resolves no `node:` builtin, so a single import here would cost the app
  // the tag. The comment about it would not be enough -- the check is on the
  // file, and it is written to fail if anybody adds one.
  // The patterns are anchored to a statement rather than to the word: this
  // file's prose discusses imports, and a substring search over comments is
  // how a scan comes to flag the sentence that explains it.
  const source = readFileSync(`${HERE}/sha256.js`, 'utf8');
  assert.equal(/^\s*import\s/m.test(source), false, 'sha256.js must import nothing');
  assert.equal(/\brequire\s*\(/.test(source), false, 'sha256.js must require nothing');
  assert.equal(/^\s*\}?\s*from\s+['"]/m.test(source), false, 'sha256.js must import from nothing');
});
