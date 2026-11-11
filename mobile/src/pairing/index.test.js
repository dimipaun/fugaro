// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The pairing module's door (mobile plan, Task P6.8).
//
// The exported surface is asserted as an **exact set**, because it is a
// decision: every name here is something a screen, the entry or a test may
// reach, and a name added without this list changing is a name nobody chose.
// The rest of the file is the door's own job -- binding the device's ports to
// the session and the screen in one call -- and the two properties that make
// the door worth having: the session it hands back is the sequenced one, and a
// missing port is refused at construction rather than at the first pairing.

import assert from 'node:assert/strict';
import test from 'node:test';

import * as pairing from './index.js';

/** The surface, sorted: the module's contract with the rest of the app. */
const SURFACE = Object.freeze([
  'API_KEY_MAX_LENGTH',
  'BASE64URL_CODES',
  'CANCEL_WORD',
  'CODE_ALPHABET',
  'CODE_BYTES',
  'CODE_HINT',
  'CODE_LABEL',
  'CODE_LENGTH',
  'CONFIG_CODES',
  'CONFIG_FIELDS',
  'DELIVERY_CODES',
  'DELIVERY_FIELDS',
  'DELIVERY_INFO',
  'DELIVERY_MIN_ENVELOPE',
  'DELIVERY_VERSION',
  'DELIVERED_SENTENCE',
  'DEVICE_FIELDS',
  'DEVICE_KEY_SIZE',
  'DEVICE_SECRET_SIZE',
  'DEVICE_TOKEN_MAX_LENGTH',
  'DEVICE_UID_BYTES',
  'DEVICE_UID_PREFIX',
  'ENVELOPE_CODES',
  'ENVELOPE_MAX_CHARS',
  'EXPIRED_SENTENCE',
  'IDLE_SENTENCE',
  'KEY_CODES',
  'LABEL_HINT',
  'PAIRING_TITLE',
  'PAIRING_UID_BYTES',
  'PAIRING_UID_PREFIX',
  'PAIR_WORD',
  'PAYLOAD_CODES',
  'PAYLOAD_FIELDS',
  'PAYLOAD_HINT',
  'PAYLOAD_MAX_LENGTH',
  'PAYLOAD_VERSION',
  'SCREEN',
  'SESSION_CODES',
  'SESSION_STATES',
  'TOKEN_MAX_LENGTH',
  'WATCHING_SENTENCE',
  'createPairing',
  'createPairingScreen',
  'createPairingSession',
  'databaseHostsFor',
  'decodeBase64url',
  'decodeDeviceKey',
  'decodeEnvelope',
  'deliveryAAD',
  'encodeBase64url',
  'encodeDeviceKey',
  'encodedLength',
  'fingerprint',
  'generateKeyPair',
  'isDeviceUID',
  'isPairingUID',
  'judgeConfig',
  'judgeFieldSet',
  'openDelivery',
  'pairingPath',
  'readDelivery',
  'readScannedPayload',
  'refusalSentence',
  'requestPath',
  'stateSentence',
  'validateDatabaseUrl',
]);

const View = function View() {};
const Text = function Text() {};
const TextInput = function TextInput() {};
const Pressable = function Pressable() {};

const React = { createElement: (type, props, ...children) => ({ type, props: props ?? null, children }) };

/** The device's ports, as far as construction is concerned: nothing here is
 * called by `createPairing`. */
function ports(over = {}) {
  return {
    x25519: { getPublicKey: () => new Uint8Array(32), getSharedSecret: () => new Uint8Array(32) },
    random: (count) => new Uint8Array(count),
    clock: { nowMs: () => 0 },
    database: { set: () => Promise.resolve({ ok: true }), watch: () => ({ ok: true, stop() {} }) },
    auth: { signIn: () => Promise.resolve({ ok: true, uid: 'p~x' }) },
    store: {},
    React,
    View,
    Text,
    TextInput,
    Pressable,
    ...over,
  };
}

test('the door exports exactly the surface this file names', () => {
  assert.deepEqual(Object.keys(pairing).sort(), [...SURFACE].sort());
});

test('createPairing binds the ports to the session and the screen', () => {
  const { session, Screen } = pairing.createPairing(ports());
  assert.equal(typeof session.start, 'function');
  assert.equal(typeof session.cancel, 'function');
  assert.equal(typeof session.snapshot, 'function');
  assert.equal(typeof Screen, 'function');
  // The session starts where every session starts: nothing scanned, no code.
  assert.equal(session.snapshot().state, pairing.SESSION_STATES.IDLE);
  assert.equal(session.snapshot().code, null);
  // The screen draws that snapshot without a credential anywhere in the tree.
  const tree = JSON.stringify(Screen({ session: session.snapshot() }));
  assert.equal(tree.includes('token'), false);
  assert.equal(tree.includes('mac'), false);
});

test('a missing port is refused at construction, not at the first pairing', () => {
  for (const name of ['x25519', 'random', 'clock', 'database', 'auth', 'store']) {
    assert.throws(() => pairing.createPairing(ports({ [name]: null })), TypeError, `${name} is required`);
  }
  for (const name of ['React', 'View', 'Text', 'TextInput', 'Pressable']) {
    assert.throws(() => pairing.createPairing(ports({ [name]: undefined })), TypeError, `${name} is required`);
  }
});
