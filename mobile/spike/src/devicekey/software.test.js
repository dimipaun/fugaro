// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The software-key fallback: it signs, it verifies, and it says of itself what
// it is (task M0; Check 733's U5).

import assert from 'node:assert/strict';
import test from 'node:test';

import { ed25519 } from '@noble/curves/ed25519.js';

import { ALGORITHM, FIXED_MESSAGE, HARDWARE_PROBE, generate, hex, report, short, signAndVerify, utf8Bytes } from './software.js';

test('the fallback key signs the fixed message and the signature verifies', () => {
  const key = generate(ed25519);
  const proof = signAndVerify(ed25519, key);
  assert.equal(proof.message, FIXED_MESSAGE);
  assert.equal(proof.signature.length, 64, 'an Ed25519 signature is 64 bytes');
  assert.equal(proof.verified, true);
  // The same message and key verify again -- the signature is not a one-shot.
  assert.equal(ed25519.verify(proof.signature, utf8Bytes(FIXED_MESSAGE), key.publicKey), true);
  // ... and one byte of the message changed is a signature that does not.
  const other = signAndVerify(ed25519, key, `${FIXED_MESSAGE}!`);
  assert.equal(ed25519.verify(other.signature, utf8Bytes(FIXED_MESSAGE), key.publicKey), false);
});

test('a key is a key and not a constant: two generations differ', () => {
  const first = generate(ed25519);
  const second = generate(ed25519);
  assert.equal(first.publicKey.length, 32);
  assert.equal(hex(first.privateKey).length, 64, 'a secret key is 32 bytes of hex');
  assert.notEqual(hex(first.privateKey), hex(second.privateKey));
  assert.notEqual(hex(first.publicKey), hex(second.publicKey));
});

test('the report answers U5 in the fallback\'s own words, and carries no private bytes', () => {
  const key = generate(ed25519);
  const proof = signAndVerify(ed25519, key);
  const read = report(key, proof);
  assert.equal(read.algorithm, ALGORITHM);
  assert.equal(read.hardwareBacked, false);
  assert.equal(read.exportable, true);
  assert.equal(read.verified, true);
  assert.equal(read.message, FIXED_MESSAGE);
  assert.equal(read.signatureBytes, 64);
  // The one thing a report must never hold: the secret. The whole report,
  // serialised, is what a screen prints and a clipboard block carries.
  const rendered = JSON.stringify(read);
  assert.equal(rendered.includes(hex(key.privateKey)), false, 'the private key is not in the report');
  // ... and the public half is, truncated, with its length said.
  assert.ok(read.publicKey.startsWith(hex(key.publicKey).slice(0, 32)));
  assert.match(read.publicKey, /\(32 bytes\)$/);
});

test('the hardware probe says what it looked for and that it found nothing', () => {
  // The finding U5 exists to produce: a screen that claimed a hardware key
  // would be claiming what the platform does not offer through Expo.
  assert.equal(HARDWARE_PROBE.found, false);
  assert.match(HARDWARE_PROBE.tried, /Keystore|Secure Enclave/);
  assert.match(HARDWARE_PROBE.why, /expo-secure-store stores items/);
});

test('a short value is cut only when it has to be, and the cut never grows the value', () => {
  assert.equal(short(Uint8Array.from([0x0a, 0xff])), '0aff');
  const bytes = Uint8Array.from({ length: 32 }, (_, i) => i);
  assert.equal(short(bytes), `${hex(bytes).slice(0, 32)}… (32 bytes)`);
  assert.equal(short(bytes, { bytes: 32 }), hex(bytes));
});

test('the message is encoded as UTF-8 by hand, so the phone needs no TextEncoder', () => {
  assert.deepEqual([...utf8Bytes('A')], [0x41]);
  assert.deepEqual([...utf8Bytes('é')], [0xc3, 0xa9]);
  assert.deepEqual([...utf8Bytes('€')], [0xe2, 0x82, 0xac]);
  assert.deepEqual([...utf8Bytes('😀')], [0xf0, 0x9f, 0x98, 0x80]);
  assert.equal(utf8Bytes('').length, 0);
});
