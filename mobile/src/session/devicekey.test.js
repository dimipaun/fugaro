// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The device key: generated on the phone, kept in platform secure storage,
// software because M0's U5 found no hardware key (task M3; O6, U5).

import assert from 'node:assert/strict';
import { createPrivateKey, createPublicKey, randomBytes, sign as nodeSign, verify as nodeVerify } from 'node:crypto';
import test from 'node:test';

import { MemorySecureStore } from '../accounts/secure-store.js';

import {
  DEVICE_KEY_ALGORITHM,
  DEVICE_KEY_CODES,
  DEVICE_KEY_SEED_BYTES,
  DEVICE_PUBLIC_KEY_BYTES,
  DEVICE_SIGNATURE_BYTES,
  DeviceKeyStore,
  deviceKeyItem,
} from './devicekey.js';

// Ed25519 in Node, which is an independent reader of the same wire format: the
// app's primitive is `@noble/curves` and the suite must not load it (no
// `node_modules`), so the test brings the platform's own Ed25519 and checks the
// bytes the app's key produces against it.
const PKCS8 = Buffer.from('302e020100300506032b657004220420', 'hex');
const SPKI = Buffer.from('302a300506032b6570032100', 'hex');

function privateKeyOf(seed) {
  return createPrivateKey({ key: Buffer.concat([PKCS8, Buffer.from(seed)]), format: 'der', type: 'pkcs8' });
}

/** The app's primitive, as the entry hands it in, over Node's own Ed25519. */
const ed25519 = Object.freeze({
  utils: Object.freeze({ randomSecretKey: () => new Uint8Array(randomBytes(DEVICE_KEY_SEED_BYTES)) }),
  getPublicKey(seed) {
    const spki = createPublicKey(privateKeyOf(seed)).export({ format: 'der', type: 'spki' });
    return new Uint8Array(spki.subarray(SPKI.length));
  },
  sign(message, seed) {
    return new Uint8Array(nodeSign(null, Buffer.from(message), privateKeyOf(seed)));
  },
  verify(signature, message, publicKey) {
    const key = createPublicKey({ key: Buffer.concat([SPKI, Buffer.from(publicKey)]), format: 'der', type: 'spki' });
    return nodeVerify(null, Buffer.from(message), key, Buffer.from(signature));
  },
});

test('the device key is software Ed25519, and M0 is where that answer lives', () => {
  // U5 found no stock Expo package that mints a hardware key, so the fallback
  // is the plan's own: pure-JavaScript Ed25519, exportable by construction.
  assert.equal(DEVICE_KEY_ALGORITHM, 'Ed25519');
  assert.equal(DEVICE_KEY_SEED_BYTES, 32);
  assert.equal(DEVICE_PUBLIC_KEY_BYTES, 32);
  assert.equal(DEVICE_SIGNATURE_BYTES, 64);
});

test('the item name is the contract’s prefix under the project id', () => {
  assert.equal(deviceKeyItem('fugaro-example'), 'fugaro.device-key.fugaro-example');
  assert.throws(() => deviceKeyItem('has space'), TypeError);
  assert.throws(() => deviceKeyItem(''), TypeError);
});

test('a created key signs bytes the platform’s Ed25519 verifies', async () => {
  const port = new MemorySecureStore();
  const store = new DeviceKeyStore({ port, ed25519 });
  const created = await store.create('fugaro-example');
  assert.equal(created.ok, true);
  assert.equal(created.publicKey.length, DEVICE_PUBLIC_KEY_BYTES);
  // The private half is in the keychain and nowhere else.
  assert.deepEqual(port.keys(), ['fugaro.device-key.fugaro-example']);

  const message = new Uint8Array([1, 2, 3, 4]);
  const signed = await store.sign('fugaro-example', message);
  assert.equal(signed.ok, true);
  assert.equal(signed.signature.length, DEVICE_SIGNATURE_BYTES);
  assert.equal(ed25519.verify(signed.signature, message, created.publicKey), true);
  // ... and one byte of the message changed is a signature that does not.
  assert.equal(ed25519.verify(signed.signature, new Uint8Array([1, 2, 3, 5]), created.publicKey), false);

  // The stored value is the seed, and it is not the public key.
  assert.equal((await store.publicKey('fugaro-example')).publicKey.length, DEVICE_PUBLIC_KEY_BYTES);
});

test('a key survives a restart, and signing needs no public key from the caller', async () => {
  const port = new MemorySecureStore();
  const first = new DeviceKeyStore({ port, ed25519 });
  const created = await first.create('fugaro-example');
  // A second store over the same keychain is the next launch.
  const second = new DeviceKeyStore({ port, ed25519 });
  const loaded = await second.publicKey('fugaro-example');
  assert.equal(loaded.ok, true);
  assert.deepEqual(loaded.publicKey, created.publicKey);
  const signed = await second.sign('fugaro-example', new Uint8Array([9]));
  assert.equal(ed25519.verify(signed.signature, new Uint8Array([9]), created.publicKey), true);
});

test('an absent key is a refusal, and a delete ends it', async () => {
  const store = new DeviceKeyStore({ port: new MemorySecureStore(), ed25519 });
  assert.equal((await store.publicKey('fugaro-example')).code, DEVICE_KEY_CODES.ABSENT);
  assert.equal((await store.sign('fugaro-example', new Uint8Array([1]))).code, DEVICE_KEY_CODES.ABSENT);
  await store.create('fugaro-example');
  assert.equal((await store.delete('fugaro-example')).ok, true);
  assert.equal((await store.publicKey('fugaro-example')).code, DEVICE_KEY_CODES.ABSENT);
});

test('a second create does not replace a key already held', async () => {
  const port = new MemorySecureStore();
  const store = new DeviceKeyStore({ port, ed25519 });
  const first = await store.create('fugaro-example');
  const again = await store.create('fugaro-example');
  assert.equal(again.code, DEVICE_KEY_CODES.ALREADY_HELD);
  assert.deepEqual((await store.publicKey('fugaro-example')).publicKey, first.publicKey);
});

test('a key that cannot be a project’s is refused before anything is written', async () => {
  const port = new MemorySecureStore();
  const store = new DeviceKeyStore({ port, ed25519 });
  assert.equal((await store.create('has space')).code, DEVICE_KEY_CODES.BAD_PROJECT);
  assert.deepEqual(port.keys(), []);
});

test('a store without the primitive refuses at construction, not at the first sign', () => {
  assert.throws(() => new DeviceKeyStore({ port: new MemorySecureStore(), ed25519: {} }), TypeError);
  assert.throws(() => new DeviceKeyStore({ port: null, ed25519 }), TypeError);
});
