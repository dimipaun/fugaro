// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The app's keeper of the licence key (subscriptions plan S2.8; the design is
// docs/licensing/licence-key.md, the S2.1 note whose "Storage and hand-off"
// section is this file's subject).
//
// S2.8's own words fix what has to hold, and each of the cases below is one of
// them:
//
//   - **"the key is kept where this task stores it and survives a restart"**:
//     where is the app's ORDINARY store and not the Keychain. D111's list is
//     two items, D232 keeps it at two (an `idToken` and a `macKey`), and both
//     are account credentials; the licence key is not one of those -- it binds
//     to an ORGANISATION rather than to a person or a machine, every
//     installation of that organisation holds the same one, and nothing
//     compares it with the installation -- so it lives under `fugaro.licence.`
//     in the unencrypted store the account tuple is kept in, and the case
//     asserts the entry name and that the store writes nothing else. A
//     **restart** is a second store built over the same port: the keys are the
//     port's, not the object's, so a store that held them in a field would
//     pass a test that never rebuilt it and lose the key the moment the app is
//     opened again.
//   - **"a bad key is refused"**: refused by the VERIFIER, whose code is the
//     answer, and refused before anything is written -- the key already
//     installed is still the one that applies afterwards. The store has no
//     second opinion about what a key is, so a bad key that the verifier would
//     refuse cannot enter the store at all.
//   - **"`SECRET_ITEMS` is still exactly `idToken,macKey`"**: the licence key
//     is the one thing that could have widened D111's list, and this is the
//     task whose review is where that would be raised. The list is asserted as
//     the constant it is, so a third item is an edit here rather than a quiet
//     one.
//
// **The keys are minted here, at run time, and the verifier is given Node's own
// Ed25519** -- the arrangement `verify.test.js` uses and for its reason: the
// suite runs in a fresh clone with no `node_modules`, so the module under test
// imports no package, and a second implementation verifies what this file
// signed. `verify.test.js`'s helpers are not imported here (a test file that
// imported another would register its cases twice); this file carries the small
// half of them it needs. No key material is written down: both pairs are
// generated in the run.
//
// The red step this file was written for: `a bad key is refused, and the key
// the app already had is untouched` fails before `src/licence/store.js` exists.

import assert from 'node:assert/strict';
import { createPublicKey, generateKeyPairSync, sign as edSign, verify as edVerify } from 'node:crypto';
import test from 'node:test';

import { SECRET_ITEMS, isSecretItem } from '../accounts/index.js';
import { LICENCE_KEYS_ENTRY, LICENCE_KEYS_MAX, LICENCE_KEY_PREFIX, LICENCE_STORE_CODES, LicenceStore } from './store.js';
import { createLicenceVerifier } from './verify.js';

const DAY = 86_400;

/** ISSUED is the moment every key here is minted at: a fixed Unix second rather
 * than a clock, because a state is a fact about a key and a `now`. */
const ISSUED = 1_800_000_000;

/** ED25519_SPKI_PREFIX is the twelve bytes every Ed25519 public key's DER
 * spelling starts with, so a raw 32-byte key can be handed to Node. */
const ED25519_SPKI_PREFIX = Buffer.from('302a300506032b6570032100', 'hex');

/** nodeEd25519 is Node's own Ed25519 as the verifier's port, over the same raw
 * public key the app's dependency takes. */
function nodeEd25519(signature, message, publicKey) {
  const key = createPublicKey({
    key: Buffer.concat([ED25519_SPKI_PREFIX, Buffer.from(publicKey)]),
    format: 'der',
    type: 'spki',
  });
  return edVerify(null, Buffer.from(message), key, Buffer.from(signature));
}

/** signer is one test key: an Ed25519 pair generated at run time, its raw
 * public half as a keyring carries it, and the id its keys are spelled under. */
function signer(id) {
  const { publicKey, privateKey } = generateKeyPairSync('ed25519');
  const raw = publicKey.export({ type: 'spki', format: 'der' });
  const half = raw.subarray(raw.length - 32);
  return { id, privateKey, keyring: { [id]: Buffer.from(half).toString('base64url') } };
}

/** mint signs one claim set as it is spelled: the payload's bytes, and a
 * signature over exactly those bytes. */
function mint(s, claims) {
  const payload = Buffer.from(JSON.stringify(claims), 'utf8');
  return [s.id, payload.toString('base64url'), edSign(null, payload, s.privateKey).toString('base64url')].join('.');
}

/** claimsFor is a claim set of the shape the design fixes, with the fields a
 * case cares about overridden. */
function claimsFor(overrides = {}) {
  return {
    tier: 'team',
    features: ['coordinator'],
    seats: 5,
    organisation: 'example.com',
    issued: ISSUED,
    expires: ISSUED + 365 * DAY,
    ...overrides,
  };
}

/** at is a `now` from a Unix second. */
function at(seconds) {
  return new Date(seconds * 1000);
}

/** The port's in-memory double: the app's ordinary store as the three methods
 * the licence store calls. It is this file's own because the store owns a
 * namespace of its own (`fugaro.licence.`) and this fake is what a case asserts
 * that namespace against. */
class MemoryStore {
  #items = new Map();

  async getItem(key) {
    return this.#items.has(key) ? this.#items.get(key) : null;
  }

  async setItem(key, value) {
    this.#items.set(key, value);
  }

  async deleteItem(key) {
    this.#items.delete(key);
  }

  /** The raw key set, which is what "where the key is kept" is read off. */
  keys() {
    return [...this.#items.keys()].sort();
  }
}

/** store is the module under test over one port and one keyring. */
function store(port, signers) {
  const keyring = {};
  for (const one of signers) Object.assign(keyring, one.keyring);
  return new LicenceStore({ port, verifier: createLicenceVerifier({ ed25519: nodeEd25519, keyring }) });
}

test('a verified key is kept where the app keeps it, and survives a restart', async () => {
  const port = new MemoryStore();
  const production = signer('prod-2026');
  const claims = claimsFor();
  const key = mint(production, claims);

  // Pasted with the whitespace a paste brings: the store keeps the text the
  // verifier read, so a key that was accepted cannot come back spelled
  // differently from the one the claims belong to.
  const installed = await store(port, [production]).install(`\n  ${key}  \n`, at(ISSUED));
  assert.deepEqual(installed, { ok: true, claims, state: 'valid' }, 'the key verified');

  // Where it is kept: one entry in the app's ordinary store, under the
  // namespace this module owns, and nothing else -- not the account tuple's
  // namespace and not the Keychain's.
  assert.deepEqual(port.keys(), [LICENCE_KEYS_ENTRY]);
  assert.equal(LICENCE_KEYS_ENTRY.startsWith(LICENCE_KEY_PREFIX), true, 'the entry is the licence namespace’s');
  assert.equal(port.keys().some((k) => k.includes('account')), false, 'nothing reached the Keychain’s namespace');

  // The restart: a second store over the same port, which is the whole of what
  // the app holds across a close and an open.
  const restarted = store(port, [production]);
  assert.deepEqual(await restarted.keys(), { ok: true, keys: [key] }, 'the key is still there');
  assert.deepEqual(await restarted.active(at(ISSUED)), { ok: true, claims, state: 'valid' }, 'and it is the one that applies');
});

test('a bad key is refused, and the key the app already had is untouched', async () => {
  const port = new MemoryStore();
  const production = signer('prod-2026');
  const stranger = signer('prod-2027');
  const held = mint(production, claimsFor());
  // The store is handed the keyring of `prod-2026` and a key spelled for an id
  // it does not hold: the id leads, so it is refused before either other part
  // is decoded (the design's rule, and S2.3's `TestVerifyUnknownKeyID`).
  const licence = store(port, [production]);
  assert.equal((await licence.install(held, at(ISSUED))).ok, true, 'the good key is installed first');

  const foreign = mint(stranger, claimsFor());
  assert.deepEqual(
    await licence.install(foreign, at(ISSUED)),
    { ok: false, code: 'unknown-key-id', field: 'id' },
    'a key this build has no public key for is refused',
  );
  assert.deepEqual(
    await licence.install('not-a-key', at(ISSUED)),
    { ok: false, code: 'malformed', field: null },
    'and a text that is not three parts is refused too',
  );

  assert.deepEqual(await licence.keys(), { ok: true, keys: [held] }, 'nothing was written for either refusal');
  assert.deepEqual(await licence.active(at(ISSUED)), { ok: true, claims: claimsFor(), state: 'valid' }, 'the installed key still applies');
});

test('a renewal installs a second key, and the later expiry is the one that applies', async () => {
  const port = new MemoryStore();
  const production = signer('prod-2026');
  const licence = store(port, [production]);

  // The first key's year, then the renewal the overlap exists for: minted while
  // the first is still valid, and expiring later (T26). Both verify, so both are
  // kept and the choice between them is `latest`'s -- the verifier's, by expiry.
  const first = mint(production, claimsFor({ expires: ISSUED + 365 * DAY }));
  const renewal = mint(production, claimsFor({ issued: ISSUED + 335 * DAY, expires: ISSUED + 700 * DAY }));
  await licence.install(first, at(ISSUED));
  await licence.install(renewal, at(ISSUED + 335 * DAY));
  assert.deepEqual(await licence.keys(), { ok: true, keys: [first, renewal] }, 'two keys are installed');

  const applied = await licence.active(at(ISSUED + 335 * DAY));
  assert.deepEqual(applied.claims.expires, ISSUED + 700 * DAY, 'the renewal is the one that applies');
  assert.equal(applied.state, 'valid', 'and it is valid while the first is still inside its term');

  // A third key arrives: the store keeps the two it may (T26's overlap installs
  // two at the most), dropping the first key -- which here is both the oldest
  // arrival and the one that expires soonest, the two rules this file separates
  // in `the cap drops the shortest term, not the newest arrival`.
  const third = mint(production, claimsFor({ issued: ISSUED + 700 * DAY, expires: ISSUED + 1065 * DAY }));
  await licence.install(third, at(ISSUED + 700 * DAY));
  assert.deepEqual(await licence.keys(), { ok: true, keys: [renewal, third] }, `at most ${LICENCE_KEYS_MAX} keys are kept`);

  // Re-installing a key already held does not spend the second slot on a
  // duplicate.
  await licence.install(third, at(ISSUED + 700 * DAY));
  assert.deepEqual(await licence.keys(), { ok: true, keys: [renewal, third] }, 'the same key twice is one key');
});

test('the cap drops the shortest term, not the newest arrival', async () => {
  const port = new MemoryStore();
  const production = signer('prod-2026');
  const licence = store(port, [production]);

  const year = mint(production, claimsFor({ expires: ISSUED + 365 * DAY }));
  const renewal = mint(production, claimsFor({ issued: ISSUED + 335 * DAY, expires: ISSUED + 700 * DAY }));
  await licence.install(year, at(ISSUED));
  await licence.install(renewal, at(ISSUED + 335 * DAY));

  // The key already held is pasted again. The set is the same two, but the year
  // key is now the newest arrival -- so a cap that counted arrivals would make
  // it the key that survives the next one, and the renewal the one that goes.
  await licence.install(year, at(ISSUED + 335 * DAY));

  // A key that expires before the renewal arrives last: an old one, out of an
  // old mail. It gives less term than the renewal and more than the year key, so
  // the cap drops the year key and the renewal the owner relies on stays.
  const stale = mint(production, claimsFor({ issued: ISSUED + 105 * DAY, expires: ISSUED + 500 * DAY }));
  await licence.install(stale, at(ISSUED + 700 * DAY));

  assert.deepEqual(
    await licence.keys(),
    { ok: true, keys: [renewal, stale] },
    'the two with the most term are the two kept, in the order they arrived',
  );
  const applied = await licence.active(at(ISSUED + 700 * DAY));
  assert.equal(applied.claims.expires, ISSUED + 700 * DAY, 'and the renewal is still the key that applies');
  assert.equal(applied.state, 'valid', 'which is the state that would have been lost with it');
});

test('two pastes at once keep both keys rather than one overwriting the other', async () => {
  const port = new MemoryStore();
  const production = signer('prod-2026');
  const licence = store(port, [production]);

  // An install reads the entry and then writes it, so two that interleaved would
  // each write a set missing the other's key -- and the store would then be
  // holding one key where the user pasted two, silently. Issued together rather
  // than one after the other, which is the whole of what this case is for.
  const first = mint(production, claimsFor({ expires: ISSUED + 365 * DAY }));
  const renewal = mint(production, claimsFor({ issued: ISSUED + 335 * DAY, expires: ISSUED + 700 * DAY }));
  const [one, two] = await Promise.all([
    licence.install(first, at(ISSUED)),
    licence.install(renewal, at(ISSUED + 335 * DAY)),
  ]);

  assert.equal(one.ok, true, 'the first paste verified');
  assert.equal(two.ok, true, 'the second paste verified');
  assert.deepEqual(await licence.keys(), { ok: true, keys: [first, renewal] }, 'both keys are kept');
  const applied = await licence.active(at(ISSUED + 335 * DAY));
  assert.equal(applied.claims.expires, ISSUED + 700 * DAY, 'and the later expiry still applies');
});

test('two stores over one store queue with each other, because the entry is one', async () => {
  const port = new MemoryStore();
  const production = signer('prod-2026');

  // The queue is the module's and not the object's, so this is the case that
  // holds it there: two store objects write the one entry, and without a queue
  // between them an install that read it before the other one wrote would drop a
  // key.
  const first = mint(production, claimsFor({ expires: ISSUED + 365 * DAY }));
  const renewal = mint(production, claimsFor({ issued: ISSUED + 335 * DAY, expires: ISSUED + 700 * DAY }));
  const [one, two] = await Promise.all([
    store(port, [production]).install(first, at(ISSUED)),
    store(port, [production]).install(renewal, at(ISSUED + 335 * DAY)),
  ]);

  assert.equal(one.ok, true, 'the first store installed its key');
  assert.equal(two.ok, true, 'the second store installed its key');
  assert.deepEqual(await store(port, [production]).keys(), { ok: true, keys: [first, renewal] }, 'both keys are kept');
});

test('an entry this store did not write is cleared rather than retried forever', async () => {
  const port = new MemoryStore();
  const production = signer('prod-2026');
  await port.setItem(LICENCE_KEYS_ENTRY, '{"not":"a list of keys"}');

  const licence = store(port, [production]);
  assert.deepEqual(await licence.keys(), { ok: true, keys: [] }, 'a blob that is not this store’s is no key');
  assert.deepEqual(port.keys(), [], 'and it is cleared, not read again on the next open');
});

test('a port that fails answers a refusal rather than the no-key state', async () => {
  const refused = new MemoryStore();
  refused.getItem = async () => {
    throw new Error('the platform store is unavailable');
  };
  const production = signer('prod-2026');
  const licence = store(refused, [production]);

  // `keys: []` would be the no-key state (S6.1's), which a screen draws as "no
  // key yet" and invites the owner to paste one -- the wrong thing to say when
  // the store could not be read at all.
  assert.deepEqual(await licence.keys(), { ok: false, code: LICENCE_STORE_CODES.STORE_FAILED, field: 'keys' });
  assert.deepEqual(await licence.active(at(ISSUED)), { ok: false, code: LICENCE_STORE_CODES.STORE_FAILED, field: 'keys' });
  assert.deepEqual(
    await licence.install(mint(production, claimsFor()), at(ISSUED)),
    { ok: false, code: LICENCE_STORE_CODES.STORE_FAILED, field: 'keys' },
    'and an install that cannot read what it would replace writes nothing',
  );
  assert.deepEqual(refused.keys(), [], 'nothing was written');
});

test('a port that cannot write refuses rather than reporting the key as kept', async () => {
  const port = new MemoryStore();
  port.setItem = async () => {
    throw new Error('the platform store is unavailable');
  };
  const production = signer('prod-2026');
  const licence = store(port, [production]);
  const key = mint(production, claimsFor());

  // The verifier's answer is `{ ok: true, claims, state }`, and the caller that
  // got it would draw the key as installed. Nothing was stored, so what comes
  // back is the store's own refusal -- the one an install that could not keep
  // the key has to answer with, or the app tells the owner a key is in place
  // that the next open will not find.
  assert.deepEqual(
    await licence.install(key, at(ISSUED)),
    { ok: false, code: LICENCE_STORE_CODES.STORE_FAILED, field: 'keys' },
    'an install that cannot write does not report the key as kept',
  );
  assert.deepEqual(port.keys(), [], 'and nothing was written');
});

test('a port that cannot clear a damaged entry refuses rather than answering no key', async () => {
  const port = new MemoryStore();
  await port.setItem(LICENCE_KEYS_ENTRY, '{"not":"a list of keys"}');
  port.deleteItem = async () => {
    throw new Error('the platform store is unavailable');
  };
  const production = signer('prod-2026');
  const licence = store(port, [production]);

  // The entry neither decodes nor can be removed. `keys: []` would be the
  // no-key state, which a screen draws as "no key yet" and invites the owner to
  // paste one -- while the blob that failed to decode is still the entry the
  // next open will read. The refusal is the answer that says so, and the case
  // asserts the entry is still there rather than quietly cleared.
  assert.deepEqual(await licence.keys(), { ok: false, code: LICENCE_STORE_CODES.STORE_FAILED, field: 'keys' });
  assert.deepEqual(port.keys(), [LICENCE_KEYS_ENTRY], 'the entry was not cleared');
  assert.deepEqual(
    await licence.active(at(ISSUED)),
    { ok: false, code: LICENCE_STORE_CODES.STORE_FAILED, field: 'keys' },
    'and reading what applies says the same',
  );
});

test('the app’s two credentials are still exactly idToken and macKey', async () => {
  // D111's list, the one this task could have widened, and D232's ruling of
  // 2026-10-29 keeps it at two -- the telemetry plan's four-item proposal was
  // refused. `website/test/privacy.mjs` reads the same constant against the
  // page, so a third item here would be a page that no longer describes the
  // app.
  assert.deepEqual(SECRET_ITEMS, ['idToken', 'macKey']);

  // And the licence key is not one of them: the store writes to the ordinary
  // store's own namespace, so there is no name under which a licence key could
  // be handed to the Keychain.
  const port = new MemoryStore();
  const production = signer('prod-2026');
  await store(port, [production]).install(mint(production, claimsFor()), at(ISSUED));
  assert.deepEqual(port.keys(), [LICENCE_KEYS_ENTRY], 'the licence key is stored where the tuple is, and nowhere else');
  assert.equal(isSecretItem('licenceKey'), false, 'and the secure store’s own fence refuses a licence key by name');
});
