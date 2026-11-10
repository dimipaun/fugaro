// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// One set of vectors for all three verifiers (subscriptions plan S2.6; the
// design is docs/licensing/licence-key.md, and S2.5 is the verifier this file
// reads them with).
//
// `ee/licence/testdata/vectors.json` is the plan's one set: the claim sets,
// states and refusals the Go verifier (S2.3), the console's (S2.4) and this
// app's (S2.5) are all read against, so a key that one of them accepts and
// another refuses is a defect no one of their own suites can see. This file
// reads that file **as it is written** -- the key texts verbatim, the ids as
// the text they are, the ring as the file spells it -- and asserts the app's
// answer for every vector and every installed-key pair. The console's sibling
// (`console/test/unit/licence-vectors.test.ts`) does the same, and the Go
// verifier's `TestVectorsRoundTrip` is the third reader.
//
// **What "the same state" means for a refusal.** The file carries a `refused`
// name for a key a verifier must turn away, drawn from the five the three
// verifiers share (`LICENCE_CODES`: malformed, unknown-key-id, signature,
// claims, lifetime), and a `state` for one it must read (valid, grace,
// expired). This file holds the app to both halves, and holds the file to
// carrying every one of the eight, so a vector set that quietly lost its
// negative half cannot be green here.
//
// **Why Node's Ed25519 is handed in here too.** The app's suite runs in a
// fresh clone with no `node_modules` (`mobile/README.md`, "Running the
// tests"), so `verify.js` imports no package and takes the primitive as a
// port; `index.js` plugs the app's own dependency in, and this file hands in
// Node's, exactly as `verify.test.js` does. The port is strict -- it refuses a
// wrong-length key or signature by throwing, as the dependency does.
//
// **No key material is in this file or the file it reads.** The vectors carry
// public halves and signed key texts and no private half anywhere: every key
// in them was minted by a throwaway keypair generated in the process that
// wrote the file.

import assert from 'node:assert/strict';
import { createPublicKey, verify as edVerify } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { LICENCE_CODES, createLicenceVerifier } from './verify.js';

const HERE = import.meta.dirname;

/** FILE is the shared vectors file, read as it is written: the same bytes the
 * Go verifier and the console read. */
const FILE = JSON.parse(readFileSync(join(HERE, '..', '..', '..', 'ee', 'licence', 'testdata', 'vectors.json'), 'utf8'));

/** NOW is the clock every vector in the file is read at. */
const NOW = new Date(FILE.now * 1000);

/** ED25519_SPKI_PREFIX is the twelve bytes every Ed25519 public key's DER
 * spelling starts with, so a raw 32-byte key -- the spelling the vectors file
 * carries -- can be handed to Node. */
const ED25519_SPKI_PREFIX = Buffer.from('302a300506032b6570032100', 'hex');

/**
 * nodeEd25519 is the port this file hands the verifier: Node's own Ed25519
 * over the same raw 32-byte public key the file's ring carries. Strict, like
 * the app's own wiring: it throws on a wrong-length key or signature rather
 * than answering `false`, which is what `@noble/curves` does.
 */
function nodeEd25519(signature, message, publicKey) {
  if (Buffer.from(publicKey).length !== 32) {
    throw new RangeError('"publicKey" expected Uint8Array of length 32');
  }
  if (Buffer.from(signature).length !== 64) {
    throw new RangeError('"signature" expected Uint8Array of length 64');
  }
  const key = createPublicKey({
    key: Buffer.concat([ED25519_SPKI_PREFIX, Buffer.from(publicKey)]),
    format: 'der',
    type: 'spki',
  });
  return edVerify(null, Buffer.from(message), key, Buffer.from(signature));
}

/** VERIFIER is the app's verifier over the vectors' own ring: the fixture id
 * and no other, which is what makes the two keyring vectors the file carries
 * read as refusals for the id alone. */
const VERIFIER = createLicenceVerifier({ ed25519: nodeEd25519, keyring: FILE.keyring });

/** REFUSAL_CODES is the five names the file may spell a refusal with, as the
 * three verifiers spell them. */
const REFUSAL_CODES = Object.values(LICENCE_CODES);

/** STATES is the three states an accepted key may be in. */
const STATES = ['valid', 'grace', 'expired'];

test('the vectors file is the one set of vectors S2.6 names, and carries both halves', () => {
  assert.equal(Object.keys(FILE.keyring).length, 1,
    'the ring holds exactly the fixture id: a second id would have to be there for a vector\'s reason');
  assert.ok(FILE.vectors.length >= 10, `the file carries ${FILE.vectors.length} vectors, and it was written to carry the negative half too`);
  assert.ok(FILE.installed.length >= 2, `the file carries ${FILE.installed.length} installed cases, and the overlap needs both`);
});

for (const vector of FILE.vectors) {
  test(`${vector.name}: the app reads it as the file says`, () => {
    if (vector.refused !== undefined) {
      assert.ok(REFUSAL_CODES.includes(vector.refused),
        `the vector names the refusal ${JSON.stringify(vector.refused)}, which is not one of the five the verifiers share`);
      const read = VERIFIER.verify(vector.key, NOW);
      assert.equal(read.ok, false, `${vector.name} verified, and the file refuses it as ${vector.refused}`);
      assert.equal(read.code, vector.refused, `${vector.name} is refused as ${read.code}, and the file says ${vector.refused}`);
      return;
    }
    assert.ok(vector.claims !== undefined, 'an accepted vector carries no claim set, so nothing holds the payload to the terms it states');
    const read = VERIFIER.verify(vector.key, NOW);
    assert.equal(read.ok, true, `${vector.name} is refused as ${read.ok ? '' : read.code}, and the file reads it as ${vector.state}`);
    assert.equal(read.state, vector.state, `${vector.name} is ${read.state}, and the file says ${vector.state}`);
    assert.deepEqual(read.claims, vector.claims, `${vector.name} reads back as different claims than the file states`);
  });
}

for (const installed of FILE.installed) {
  test(`${installed.name}: the app picks the key the file says applies`, () => {
    const applies = VERIFIER.verify(installed.keys[installed.applies], NOW);
    assert.equal(applies.ok, true, `the key this case says applies is refused: ${applies.ok ? '' : applies.code}`);

    // The pair is only interesting while both keys are good, so a case whose
    // other key were refused would prove nothing about the choice.
    for (const [name, key] of Object.entries(installed.keys)) {
      if (name === installed.applies) continue;
      assert.equal(VERIFIER.verify(key, NOW).ok, true, `the ${name} key of the pair is refused too, so the choice is not what this case measures`);
    }

    assert.deepEqual(VERIFIER.latest(Object.values(installed.keys), NOW), applies,
      `${installed.name}: the store applied another key`);

    // A map has no order, so the file's order is not the order the store reads:
    // the pair the other way round is the same answer.
    const reversed = [installed.keys[installed.applies], ...Object.entries(installed.keys)
      .filter(([name]) => name !== installed.applies)
      .map(([, key]) => key)];
    assert.deepEqual(VERIFIER.latest(reversed, NOW), applies,
      `${installed.name}: with the keys the other way round the store applied another key`);
    assert.equal(VERIFIER.latest(Object.values(installed.keys), NOW).state, installed.state,
      `${installed.name}: the store answered another state than the file's ${installed.state}`);
  });
}

test('the file carries every refusal and every state the three verifiers share', () => {
  const seen = new Set(FILE.vectors.map((vector) => vector.refused ?? vector.state));
  for (const code of REFUSAL_CODES) {
    assert.ok(seen.has(code), `no vector in the file is refused as ${code}`);
  }
  for (const state of STATES) {
    assert.ok(seen.has(state), `no vector in the file is ${state}`);
  }
});
