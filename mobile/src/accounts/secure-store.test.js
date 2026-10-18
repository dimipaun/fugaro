// The secret store's tests (mobile plan, Task P3.1; decision D111).
//
// D111's "both in platform secure storage, and nothing else" is a claim about
// an API no test in this repository can call -- there is no Keychain in CI --
// so the platform sits behind a port (Voice D52: fakes from task one) and this
// file pins every rule this repository made about the items:
//
//   * the key a value lands under is composed from a VALIDATED account id and
//     a known item name, and an id that could carry the separator is refused
//     before anything is composed;
//   * **only a `Secret` can be written**: a bare string, a look-alike object
//     and a `Secret` for the other item are each refused, so the Keychain
//     cannot be reached by a path that skipped `secret.js`;
//   * a write the platform rejects is a refusal (`store-failed`) and NOT a
//     success, and the refusal does not carry the value;
//   * a read that comes back as something this module could not have written
//     is `unreadable`, never repaired into a credential;
//   * one account's keys are not another's -- `forget` reaches exactly one
//     account, and the key SET of two configured accounts shares nothing.
//
// The values are synthetic and shaped nothing like a credential: no fixture in
// this directory is recorded from a real installation (`belong` least of all,
// Global Constraint 11).

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  MemorySecureStore,
  SECRET_KEY_PREFIX,
  SECURE_STORE_CODES,
  SecretStore,
  secretKey,
} from './secure-store.js';
import { Secret, secret } from './secret.js';

const ID_TOKEN = 'synthetic-id-token';
const MAC_KEY = 'synthetic-mac-key';

/** A store over a fresh memory port, and the port itself for inspection. */
function makeStore() {
  const port = new MemorySecureStore();
  return { port, store: new SecretStore(port) };
}

const keyOf = (accountId, item) => `${SECRET_KEY_PREFIX}${accountId}.${item}`;

test('the key is composed of the prefix, a validated id and a known item', () => {
  assert.equal(secretKey('laptop', 'idToken'), keyOf('laptop', 'idToken'));
  // Composing from input it cannot check throws rather than guessing: a key
  // that could point at another account is not a key this module writes.
  assert.throws(() => secretKey('a.b', 'idToken'), TypeError);
  assert.throws(() => secretKey('', 'idToken'), TypeError);
  assert.throws(() => secretKey('laptop', 'password'), TypeError);
  assert.throws(() => secretKey(7, 'idToken'), TypeError);
});

test('a Secret is written under its account\'s own key, verbatim', async () => {
  const { port, store } = makeStore();
  const result = await store.put('laptop', 'idToken', secret('idToken', ID_TOKEN).secret);
  assert.equal(result.ok, true);
  assert.deepEqual(port.keys(), [keyOf('laptop', 'idToken')]);
  assert.equal(await port.getItem(keyOf('laptop', 'idToken')), ID_TOKEN);
});

test('only a Secret can reach the keychain: a bare string is refused', async () => {
  const { port, store } = makeStore();
  assert.equal((await store.put('laptop', 'idToken', ID_TOKEN)).code, SECURE_STORE_CODES.NOT_A_SECRET);
  assert.equal(
    (await store.put('laptop', 'idToken', { item: 'idToken', reveal: () => ID_TOKEN })).code,
    SECURE_STORE_CODES.NOT_A_SECRET,
  );
  assert.deepEqual(port.keys(), [], 'nothing may be written by a refused call');
});

test('the item must match: a MAC key written as a token is refused', async () => {
  const { port, store } = makeStore();
  const result = await store.put('laptop', 'idToken', secret('macKey', MAC_KEY).secret);
  assert.equal(result.code, SECURE_STORE_CODES.ITEM_MISMATCH);
  assert.equal(result.field, 'item');
  assert.deepEqual(port.keys(), []);
});

test('an id or an item the store does not know is refused, and writes nothing', async () => {
  const { port, store } = makeStore();
  const value = secret('idToken', ID_TOKEN).secret;
  for (const id of ['', 'a.b', 'a b', 'x'.repeat(65), 7, null]) {
    assert.equal((await store.put(id, 'idToken', value)).code, SECURE_STORE_CODES.BAD_ACCOUNT, String(id));
  }
  assert.equal((await store.put('laptop', 'password', value)).code, SECURE_STORE_CODES.UNKNOWN_ITEM);
  assert.equal((await store.get('a.b', 'idToken')).code, SECURE_STORE_CODES.BAD_ACCOUNT);
  assert.equal((await store.items('a.b')).code, SECURE_STORE_CODES.BAD_ACCOUNT);
  assert.equal((await store.forget('a.b')).code, SECURE_STORE_CODES.BAD_ACCOUNT);
  assert.deepEqual(port.keys(), []);
});

test('a write the platform rejects is a refusal, not a success', async () => {
  const failing = {
    async getItem() { return null; },
    async setItem() { throw new Error(`keychain said no to ${ID_TOKEN}`); },
    async deleteItem() {},
  };
  const store = new SecretStore(failing);
  const result = await store.put('laptop', 'idToken', secret('idToken', ID_TOKEN).secret);
  assert.equal(result.ok, false);
  assert.equal(result.code, SECURE_STORE_CODES.STORE_FAILED);
  // The platform's own message may quote the value; the refusal must not.
  assert.equal(JSON.stringify(result).includes(ID_TOKEN), false);
});

test('a read comes back wrapped, and absence is its own answer', async () => {
  const { store } = makeStore();
  const absent = await store.get('laptop', 'idToken');
  assert.equal(absent.ok, false);
  assert.equal(absent.code, SECURE_STORE_CODES.ABSENT);
  await store.put('laptop', 'idToken', secret('idToken', ID_TOKEN).secret);
  const read = await store.get('laptop', 'idToken');
  assert.equal(read.ok, true);
  assert.equal(read.secret.item, 'idToken');
  assert.equal(read.secret.reveal(), ID_TOKEN);
  assert.equal(read.secret instanceof Secret, true);
});

test('a stored value this module could not have written is unreadable, not repaired', async () => {
  const { port, store } = makeStore();
  for (const planted of ['', 'has space', 'x'.repeat(2049), 'café']) {
    await port.setItem(keyOf('laptop', 'idToken'), planted);
    const read = await store.get('laptop', 'idToken');
    assert.equal(read.code, SECURE_STORE_CODES.UNREADABLE, JSON.stringify(planted));
    assert.equal(read.secret, undefined);
  }
});

test('items() reports presence, and reads both names and no others', async () => {
  const { port, store } = makeStore();
  assert.deepEqual((await store.items('laptop')).items, []);
  await store.put('laptop', 'macKey', secret('macKey', MAC_KEY).secret);
  assert.deepEqual((await store.items('laptop')).items, ['macKey']);
  await store.put('laptop', 'idToken', secret('idToken', ID_TOKEN).secret);
  assert.deepEqual((await store.items('laptop')).items, ['idToken', 'macKey']);
  assert.deepEqual(port.keys(), [keyOf('laptop', 'idToken'), keyOf('laptop', 'macKey')]);
});

test('forget removes both items of ONE account and touches no other', async () => {
  const { port, store } = makeStore();
  await store.put('laptop', 'idToken', secret('idToken', ID_TOKEN).secret);
  await store.put('laptop', 'macKey', secret('macKey', MAC_KEY).secret);
  await store.put('phone', 'idToken', secret('idToken', 'synthetic-other-token').secret);

  const forgotten = await store.forget('laptop');
  assert.equal(forgotten.ok, true);
  assert.deepEqual([...forgotten.removed], ['idToken', 'macKey']);
  assert.deepEqual(port.keys(), [keyOf('phone', 'idToken')]);
  assert.equal((await store.get('phone', 'idToken')).secret.reveal(), 'synthetic-other-token');
});

test('there is no shared token store: every key belongs to exactly one account', async () => {
  const { port, store } = makeStore();
  await store.put('laptop', 'idToken', secret('idToken', ID_TOKEN).secret);
  await store.put('phone', 'idToken', secret('idToken', 'synthetic-other-token').secret);
  const keys = port.keys();
  assert.equal(keys.length, 2);
  for (const key of keys) {
    assert.equal(key.startsWith(SECRET_KEY_PREFIX), true, key);
    const owners = ['laptop', 'phone'].filter((id) => key === keyOf(id, key.split('.').pop()));
    assert.equal(owners.length, 1, `${key} must name one account`);
  }
  // The values are distinct, so neither read can be answering with the other's.
  assert.notEqual(
    (await store.get('laptop', 'idToken')).secret.reveal(),
    (await store.get('phone', 'idToken')).secret.reveal(),
  );
});
