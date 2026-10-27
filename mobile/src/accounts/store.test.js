// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The account store's tests (mobile plan, Task P3.1; D114, D111).
//
// This is the file that pins the plan's own sentences for P3.1:
//
//   * "**An account is an installation**: one Firebase project, one operator
//     identity, one Fugaro project name (D114)" -- and two entries for ONE
//     installation are refused, because that is D114's "non-overlapping" in
//     the quiet direction;
//   * "the store holds several, non-overlapping: a per-account secure-storage
//     item, a per-account cache namespace, **no shared token store**, and a
//     switcher that **tears down the previous account's listeners and caches
//     before it opens the next**" -- asserted as a property, on two configured
//     accounts: no read from one returns a value belonging to the other, and a
//     switch leaves no listener from the previous account alive;
//   * "**`belong` is added by hand**" -- so a fresh store is EMPTY, `add` is
//     the only way in, and there is no discovery and no seed account (Global
//     Constraint 11: no agent touches `belong`, and no fixture is ever
//     recorded from it);
//   * the public view of an account carries D114's tuple and **no secret**,
//     and the two items are reachable only one at a time, by account and by
//     item.
//
// The values are synthetic and shaped nothing like a credential: no fixture in
// this directory is recorded from a real installation.

import assert from 'node:assert/strict';
import test from 'node:test';

import { ACCOUNT_FIELDS } from './account.js';
import { ResourceRegistry } from './resources.js';
import { MemorySecureStore, SECRET_KEY_PREFIX, SecretStore } from './secure-store.js';
import { SECRET_ITEMS, secret } from './secret.js';
import { FORBIDDEN_KEY_SEGMENTS, MemorySettingsStore, SETTINGS_KEY_PREFIX, SettingsStore } from './settings.js';
import { ACCOUNT_KEY_PREFIX, STORE_CODES, AccountStore } from './store.js';

const LAPTOP = Object.freeze({
  id: 'laptop',
  firebaseProjectId: 'fugaro-dev',
  authUid: 'uid-0001',
  fugaroProject: 'fugaro',
});
const PHONE = Object.freeze({
  id: 'phone',
  firebaseProjectId: 'fugaro-sandbox',
  authUid: 'uid-0002',
  fugaroProject: 'sandbox',
});

const LAPTOP_TOKEN = 'synthetic-id-token-laptop';
const PHONE_TOKEN = 'synthetic-id-token-phone';
const LAPTOP_MAC = 'synthetic-mac-key-laptop';

/** A store over fresh in-memory ports, with the raw stores for inspection and
 * the registry's trace for the ordering assertions. */
function makeStore({ trace } = {}) {
  const secretPort = new MemorySecureStore();
  const settingsPort = new MemorySettingsStore();
  const secrets = new SecretStore(secretPort);
  const settings = new SettingsStore(settingsPort);
  const registry = new ResourceRegistry(trace === undefined ? {} : { trace });
  return {
    secretPort,
    settingsPort,
    secrets,
    settings,
    registry,
    store: new AccountStore({ secrets, settings, registry }),
  };
}

test('a fresh store holds nothing: the account list is typed by the owner', async () => {
  const { store } = makeStore();
  const listed = await store.list();
  assert.deepEqual([...listed.accounts], []);
  assert.equal(store.current(), null, 'no account is open until the owner adds one');
  // A settings key that is not an account is not an account either: there is
  // no discovery and no seed entry.
  const other = makeStore();
  await other.settings.set('ui.theme', 'dark');
  assert.deepEqual([...(await other.store.list()).accounts], []);
});

test('adding an installation lists it, as D114\'s tuple and nothing else', async () => {
  const { store } = makeStore();
  assert.equal((await store.add(LAPTOP)).ok, true);
  const listed = await store.list();
  assert.deepEqual([...listed.accounts], [LAPTOP]);
  assert.deepEqual(Object.keys(listed.accounts[0]), [...ACCOUNT_FIELDS]);
  assert.equal(Object.isFrozen(listed.accounts[0]), true);
  const one = await store.account('laptop');
  assert.deepEqual(one.account, LAPTOP);
});

test('the owner may name an installation any word the alphabet allows', async () => {
  // Review finding, end to end: these are exactly the ids `add` used to refuse
  // with `secret-key`, because the settings key fence was judging the owner's
  // own word with its own word list. Verified by running `add` with each of
  // them, and walked over the list rather than over the one word that was
  // noticed.
  const { store } = makeStore();
  for (const id of [...FORBIDDEN_KEY_SEGMENTS, ...SECRET_ITEMS, 'mac', 'belong']) {
    const installation = {
      id,
      firebaseProjectId: `project-for-${id}`,
      authUid: `uid-${id}`,
      fugaroProject: `fugaro-${id}`,
    };
    const added = await store.add(installation);
    assert.equal(added.ok, true, `${id}: ${added.code ?? ''}`);
    assert.deepEqual((await store.account(id)).account, installation);
    // The tuple round-trips through the disk, so the key it was written under
    // is one the store can read back -- not merely one it accepted.
    assert.ok((await store.list()).accounts.some((held) => held.id === id), id);
  }
});

test('a tuple that is not an installation is refused, with its own code', async () => {
  const { store } = makeStore();
  assert.equal((await store.add({ id: 'laptop' })).code, 'missing-field');
  assert.equal((await store.add({ ...LAPTOP, id: 'a.b' })).code, 'bad-id');
  assert.deepEqual([...(await store.list()).accounts], []);
});

test('a second entry for one installation is refused, however it is named', async () => {
  const { store } = makeStore();
  await store.add(LAPTOP);
  assert.equal((await store.add(LAPTOP)).code, STORE_CODES.DUPLICATE_ID);
  // The same installation under another id: two cache namespaces and two
  // Keychain items for one project -- D114's "non-overlapping", broken.
  assert.equal(
    (await store.add({ ...LAPTOP, id: 'laptop-2' })).code,
    STORE_CODES.DUPLICATE_INSTALLATION,
  );
  // A different installation is a different account, and is accepted.
  assert.equal((await store.add(PHONE)).ok, true);
  assert.deepEqual((await store.list()).accounts.map((a) => a.id), ['laptop', 'phone']);
});

test('the items are written and read one account and one item at a time', async () => {
  const { store, secretPort } = makeStore();
  await store.add(LAPTOP);
  assert.deepEqual((await store.items('laptop')).items, []);
  assert.equal(
    (await store.setSecret('laptop', 'idToken', secret('idToken', LAPTOP_TOKEN).secret)).ok,
    true,
  );
  await store.setSecret('laptop', 'macKey', secret('macKey', LAPTOP_MAC).secret);
  assert.deepEqual((await store.items('laptop')).items, ['idToken', 'macKey']);
  assert.equal((await store.secret('laptop', 'idToken')).secret.reveal(), LAPTOP_TOKEN);
  assert.deepEqual(secretPort.keys(), [
    `${SECRET_KEY_PREFIX}laptop.idToken`,
    `${SECRET_KEY_PREFIX}laptop.macKey`,
  ]);
});

test('a secret for an account or an item the app does not have is refused', async () => {
  const { store } = makeStore();
  await store.add(LAPTOP);
  const value = secret('idToken', LAPTOP_TOKEN).secret;
  assert.equal((await store.setSecret('phone', 'idToken', value)).code, STORE_CODES.UNKNOWN_ACCOUNT);
  assert.equal((await store.secret('phone', 'idToken')).code, STORE_CODES.UNKNOWN_ACCOUNT);
  assert.equal((await store.setSecret('laptop', 'password', value)).code, 'unknown-item');
  assert.equal((await store.secret('laptop', 'password')).code, 'unknown-item');
  assert.equal((await store.items('phone')).code, STORE_CODES.UNKNOWN_ACCOUNT);
});

test('the public view of an account carries no secret, in any serialisation', async () => {
  const { store } = makeStore();
  await store.add(LAPTOP);
  await store.add(PHONE);
  await store.setSecret('laptop', 'idToken', secret('idToken', LAPTOP_TOKEN).secret);
  await store.setSecret('phone', 'idToken', secret('idToken', PHONE_TOKEN).secret);
  const listed = await store.list();
  const text = JSON.stringify(listed);
  assert.equal(text.includes(LAPTOP_TOKEN), false);
  assert.equal(text.includes(PHONE_TOKEN), false);
  assert.equal(text.includes('secret'), false, 'no field of the view names a secret at all');
  assert.equal((await store.account('laptop')).account.macKey, undefined);
});

test('there is no shared token store: the settings file holds no secret key', async () => {
  const { store, secretPort, settingsPort } = makeStore();
  await store.add(LAPTOP);
  await store.setSecret('laptop', 'idToken', secret('idToken', LAPTOP_TOKEN).secret);
  for (const key of settingsPort.keys()) {
    assert.equal(key.startsWith(SECURE_STORE_PREFIX_FOR_TEST), false, key);
  }
  for (const key of secretPort.keys()) {
    assert.equal(key.startsWith(SETTINGS_KEY_PREFIX), false, key);
    assert.equal(key.startsWith(SECRET_KEY_PREFIX), true, key);
  }
  assert.deepEqual(settingsPort.keys(), [`${SETTINGS_KEY_PREFIX}${ACCOUNT_KEY_PREFIX}laptop`]);
});

// The secure store's prefix, written once here so the assertion above reads as
// a sentence; `boundary.test.js` is what keeps the literal in one module.
const SECURE_STORE_PREFIX_FOR_TEST = 'fugaro.account.';

test('switching closes the previous account before it opens the next', async () => {
  const trace = [];
  const { store, registry } = makeStore({ trace });
  await store.add(LAPTOP);
  await store.add(PHONE);
  await store.switchTo('laptop');
  const stopped = [];
  registry.add('laptop', () => stopped.push('laptop-listener'));
  assert.equal(registry.liveCount('laptop'), 1);

  const switched = await store.switchTo('phone');
  assert.equal(switched.ok, true);
  assert.equal(switched.changed, true);
  assert.equal(store.current(), 'phone');
  // The previous account's listener is stopped and its cache is gone ...
  assert.deepEqual(stopped, ['laptop-listener']);
  assert.equal(registry.liveCount('laptop'), 0);
  assert.equal(registry.cacheFor('laptop').code, 'not-open');
  // ... and it happened BEFORE the next account was opened.
  assert.deepEqual(trace, [
    ['open', 'laptop'],
    ['listener', 'laptop'],
    ['close', 'laptop'],
    ['open', 'phone'],
  ]);
  assert.deepEqual(registry.openAccounts(), ['phone']);
});

test('closing with nothing open is a no-op, and closes nothing', async () => {
  // Review finding: this was the only public method on the store with no test
  // — and it is the second entrance to the same close-before-open teardown
  // `switchTo` has, reached when the app goes to the background.
  const trace = [];
  const { store, registry } = makeStore({ trace });
  const closed = await store.closeCurrent();
  assert.equal(closed.ok, true);
  assert.equal(closed.changed, false, 'there was nothing to close');
  assert.deepEqual(trace, [], 'and nothing was torn down to say so');
  assert.equal(store.current(), null);

  await store.add(LAPTOP);
  await store.switchTo('laptop');
  registry.add('laptop', () => {});
  const once = await store.closeCurrent();
  assert.equal(once.changed, true);
  assert.equal(store.current(), null);
  assert.equal(registry.liveCount('laptop'), 0);
  assert.equal(registry.cacheFor('laptop').code, 'not-open');
  // Idempotent from here on: a second close changes nothing.
  assert.equal((await store.closeCurrent()).changed, false);
});

test('a close whose teardown failed still leaves no account open', async () => {
  const { store, registry } = makeStore();
  await store.add(LAPTOP);
  await store.switchTo('laptop');
  registry.add('laptop', () => { throw new Error('socket already gone'); });

  const closed = await store.closeCurrent();
  assert.equal(closed.ok, false);
  assert.equal(closed.code, STORE_CODES.TEARDOWN_FAILED);
  assert.equal(closed.accountId, 'laptop');
  assert.equal(closed.failures, 1);
  // The invariant the whole module rests on: a failed teardown is never a
  // reason to say an account is open when nothing is subscribed. Fail closed
  // in the direction that has no listener alive.
  assert.equal(store.current(), null);
  assert.deepEqual(registry.openAccounts(), []);
  assert.equal(registry.liveCount('laptop'), 0);
  // ... and the failure is reported, not remembered as an open account: the
  // next close has nothing left to do.
  assert.equal((await store.closeCurrent()).changed, false);
});

test('switching to the account that is already open changes nothing', async () => {
  const trace = [];
  const { store, registry } = makeStore({ trace });
  await store.add(LAPTOP);
  await store.switchTo('laptop');
  registry.add('laptop', () => {});
  const again = await store.switchTo('laptop');
  assert.equal(again.changed, false);
  assert.equal(registry.liveCount('laptop'), 1, 'no teardown of the account that stays open');
  assert.deepEqual(trace, [['open', 'laptop'], ['listener', 'laptop']]);
});

test('a teardown that failed does not open the next account', async () => {
  const { store, registry } = makeStore();
  await store.add(LAPTOP);
  await store.add(PHONE);
  await store.switchTo('laptop');
  registry.add('laptop', () => { throw new Error('socket already gone'); });

  const switched = await store.switchTo('phone');
  assert.equal(switched.ok, false);
  assert.equal(switched.code, STORE_CODES.TEARDOWN_FAILED);
  // No account is open: the previous one is closed and the next was never
  // opened, so there is no moment with two installations live.
  assert.equal(store.current(), null);
  assert.deepEqual(registry.openAccounts(), []);
  assert.equal(registry.liveCount('laptop'), 0);
});

test('switching to an account the app does not hold is refused', async () => {
  const { store, registry } = makeStore();
  await store.add(LAPTOP);
  assert.equal((await store.switchTo('phone')).code, STORE_CODES.UNKNOWN_ACCOUNT);
  assert.deepEqual(registry.openAccounts(), []);
  assert.equal((await store.switchTo('a.b')).code, STORE_CODES.BAD_ACCOUNT);
});

test('two configured accounts never answer with each other\'s values', async () => {
  const { store, registry } = makeStore();
  await store.add(LAPTOP);
  await store.add(PHONE);
  await store.setSecret('laptop', 'idToken', secret('idToken', LAPTOP_TOKEN).secret);
  await store.setSecret('phone', 'idToken', secret('idToken', PHONE_TOKEN).secret);

  assert.equal((await store.secret('laptop', 'idToken')).secret.reveal(), LAPTOP_TOKEN);
  assert.equal((await store.secret('phone', 'idToken')).secret.reveal(), PHONE_TOKEN);
  assert.notEqual(
    (await store.secret('laptop', 'idToken')).secret.reveal(),
    (await store.secret('phone', 'idToken')).secret.reveal(),
  );

  await store.switchTo('laptop');
  const laptopCache = registry.cacheFor('laptop').cache;
  laptopCache.set('feed', 'laptop value');
  await store.switchTo('phone');
  const phoneCache = registry.cacheFor('phone').cache;
  assert.equal(phoneCache.get('feed'), undefined, 'the next account starts with an empty cache');
  phoneCache.set('feed', 'phone value');
  assert.equal(phoneCache.get('feed'), 'phone value');
  assert.notEqual(laptopCache.get('feed'), phoneCache.get('feed'));
});

test('removing an account forgets its secrets, stops its listeners and drops it', async () => {
  const { store, registry, secretPort, settingsPort } = makeStore();
  await store.add(LAPTOP);
  await store.add(PHONE);
  await store.setSecret('laptop', 'idToken', secret('idToken', LAPTOP_TOKEN).secret);
  await store.setSecret('phone', 'idToken', secret('idToken', PHONE_TOKEN).secret);
  await store.switchTo('laptop');
  const stopped = [];
  registry.add('laptop', () => stopped.push('laptop-listener'));

  const removed = await store.remove('laptop');
  assert.equal(removed.ok, true);
  assert.deepEqual([...removed.removed], ['idToken']);
  assert.deepEqual(stopped, ['laptop-listener']);
  assert.equal(store.current(), null);
  assert.deepEqual((await store.list()).accounts.map((a) => a.id), ['phone']);
  assert.deepEqual(secretPort.keys(), [`${SECRET_KEY_PREFIX}phone.idToken`]);
  assert.deepEqual(settingsPort.keys(), [`${SETTINGS_KEY_PREFIX}${ACCOUNT_KEY_PREFIX}phone`]);
  assert.equal((await store.secret('laptop', 'idToken')).code, STORE_CODES.UNKNOWN_ACCOUNT);
});

test('a removal whose secret round fails removes nothing at all', async () => {
  const failing = (port) => ({
    async getItem(key) { return port.getItem(key); },
    async setItem(key, value) { return port.setItem(key, value); },
    async deleteItem() { throw new Error('keychain said no'); },
    async keys() { return port.keys(); },
  });
  const settingsPort = new MemorySettingsStore();
  const registry = new ResourceRegistry();
  const store = new AccountStore({
    secrets: new SecretStore(failing(new MemorySecureStore())),
    settings: new SettingsStore(settingsPort),
    registry,
  });
  await store.add(LAPTOP);
  assert.equal((await store.setSecret('laptop', 'idToken', secret('idToken', LAPTOP_TOKEN).secret)).ok, true);
  const removed = await store.remove('laptop');
  assert.equal(removed.ok, false);
  assert.equal(removed.code, 'store-failed');
  // The account is intact, so it can be removed again: a credential left with
  // no account pointing at it would never be deleted.
  assert.deepEqual((await store.list()).accounts.map((a) => a.id), ['laptop']);
  assert.equal(registry.isOpen('laptop'), false);
});

test('a removal while the keychain is locked leaves no credential behind', async () => {
  // The reviewer's repro, as a test: a locked phone cannot READ its items
  // (`WHEN_UNLOCKED_THIS_DEVICE_ONLY`), and an earlier `forget` skipped every
  // item whose read failed -- so the removal returned ok, the tuple went, and
  // both items stayed in the Keychain with no account able to reach them.
  const inner = new MemorySecureStore();
  const locked = {
    async getItem() { throw new Error('keychain is locked'); },
    async setItem(key, value) { return inner.setItem(key, value); },
    async deleteItem(key) { return inner.deleteItem(key); },
    async keys() { return inner.keys(); },
  };
  const settingsPort = new MemorySettingsStore();
  const store = new AccountStore({
    secrets: new SecretStore(locked),
    settings: new SettingsStore(settingsPort),
    registry: new ResourceRegistry(),
  });
  await store.add(LAPTOP);
  await inner.setItem(`${SECRET_KEY_PREFIX}laptop.idToken`, LAPTOP_TOKEN);
  await inner.setItem(`${SECRET_KEY_PREFIX}laptop.macKey`, LAPTOP_MAC);

  const removed = await store.remove('laptop');
  assert.equal(removed.ok, true);
  assert.deepEqual(inner.keys(), [], 'no credential outlives the account that owned it');
  assert.deepEqual((await store.list()).accounts, []);
  assert.equal(store.current(), null);
});

test('removing an account the app does not hold is refused', async () => {
  const { store } = makeStore();
  assert.equal((await store.remove('phone')).code, STORE_CODES.UNKNOWN_ACCOUNT);
});

test('a stored tuple this module could not have written fails the whole list', async () => {
  const { store, settingsPort } = makeStore();
  await store.add(LAPTOP);
  await settingsPort.setItem(
    `${SETTINGS_KEY_PREFIX}${ACCOUNT_KEY_PREFIX}phone`,
    JSON.stringify({ ...PHONE, id: 'somebody-else' }),
  );
  const listed = await store.list();
  assert.equal(listed.ok, false);
  assert.equal(listed.code, STORE_CODES.UNREADABLE_ACCOUNT);
});

test('a stored tuple that is malformed is refused on every read path, never handed out', async () => {
  // Each record below is valid JSON and passes the settings store's own
  // fences, so only the account store's re-validation stands between it and
  // a caller: an extra field, a missing field, a field of the wrong type and a
  // padded field. (An extra field NAMED like an item is caught a layer earlier,
  // by the settings store's key-word fence.)
  const malformed = [
    { ...PHONE, endpoint: 'https://example.invalid' },
    { id: PHONE.id, firebaseProjectId: PHONE.firebaseProjectId, authUid: PHONE.authUid },
    { ...PHONE, authUid: 2 },
    { ...PHONE, fugaroProject: ' sandbox' },
  ];
  for (const record of malformed) {
    const { store, settingsPort, registry } = makeStore();
    await settingsPort.setItem(`${SETTINGS_KEY_PREFIX}${ACCOUNT_KEY_PREFIX}phone`, JSON.stringify(record));
    const label = JSON.stringify(record);
    assert.equal((await store.account('phone')).code, STORE_CODES.UNREADABLE_ACCOUNT, label);
    assert.equal((await store.list()).code, STORE_CODES.UNREADABLE_ACCOUNT, label);
    const switched = await store.switchTo('phone');
    assert.equal(switched.code, STORE_CODES.UNREADABLE_ACCOUNT, label);
    assert.equal(store.current(), null, label);
    assert.deepEqual(registry.openAccounts(), [], label);
    assert.equal((await store.secret('phone', 'idToken')).code, STORE_CODES.UNREADABLE_ACCOUNT, label);
  }
});

test('two concurrent adds of one installation admit exactly one entry', async () => {
  const { store } = makeStore();
  const results = await Promise.all([store.add(LAPTOP), store.add({ ...LAPTOP, id: 'laptop-again' })]);
  assert.deepEqual(
    results.map((r) => r.ok).sort(),
    [false, true],
    'one add wins and the other is refused, whatever the interleaving',
  );
  assert.equal(results.find((r) => !r.ok).code, STORE_CODES.DUPLICATE_INSTALLATION);
  assert.equal((await store.list()).accounts.length, 1);
});

test('a switch racing a removal never leaves the removed account open', async () => {
  const { store, registry } = makeStore();
  await store.add(LAPTOP);
  await store.add(PHONE);
  await store.switchTo('phone');
  // The removal starts first; the switch to the same account is issued while
  // the removal is still awaiting the stores.
  const [removed, switched] = await Promise.all([store.remove('laptop'), store.switchTo('laptop')]);
  assert.equal(removed.ok, true);
  assert.equal(switched.ok, false, 'the switch sees the account already removed');
  assert.equal(switched.code, STORE_CODES.UNKNOWN_ACCOUNT);
  assert.equal(registry.isOpen('laptop'), false);
  assert.notEqual(store.current(), 'laptop');
  assert.deepEqual([...(await store.list()).accounts].map((a) => a.id), ['phone']);
});

test('a switch closes every account open in the registry, not only the one it recorded', async () => {
  const trace = [];
  const { store, registry } = makeStore({ trace });
  await store.add(LAPTOP);
  await store.add(PHONE);
  // An account opened on the registry behind the store's back (a later
  // task's reader, or a bug) must not survive the next switch.
  registry.open('phone');
  const stopped = [];
  registry.add('phone', () => stopped.push('phone-listener'));

  const switched = await store.switchTo('laptop');
  assert.equal(switched.ok, true);
  assert.deepEqual(stopped, ['phone-listener']);
  assert.deepEqual(registry.openAccounts(), ['laptop']);
  assert.deepEqual(trace.slice(-2), [
    ['close', 'phone'],
    ['open', 'laptop'],
  ]);

  // ... and closeCurrent leaves nothing open either.
  registry.open('phone');
  const closed = await store.closeCurrent();
  assert.equal(closed.ok, true);
  assert.deepEqual(registry.openAccounts(), []);
  assert.equal(store.current(), null);
});

test('a secret written while the account is being removed leaves no orphan credential', async () => {
  // The removal is held at its last step -- the tuple's delete -- after it has
  // already forgotten the account's items. A write issued at that moment
  // reads a tuple that is still there; unless it waits for the removal, it
  // puts a credential in the Keychain for an account about to vanish.
  let release;
  const gate = new Promise((resolve) => { release = resolve; });
  let reachedDelete;
  const atDelete = new Promise((resolve) => { reachedDelete = resolve; });
  class HeldSettings extends MemorySettingsStore {
    async deleteItem(key) {
      reachedDelete();
      await gate;
      return super.deleteItem(key);
    }
  }
  const secretPort = new MemorySecureStore();
  const store = new AccountStore({
    secrets: new SecretStore(secretPort),
    settings: new SettingsStore(new HeldSettings()),
    registry: new ResourceRegistry(),
  });
  await store.add(LAPTOP);
  const removal = store.remove('laptop');
  await atDelete;
  const write = store.setSecret('laptop', 'idToken', secret('idToken', LAPTOP_TOKEN).secret);
  release();
  const [removed, written] = await Promise.all([removal, write]);
  assert.equal(removed.ok, true);
  assert.equal(written.ok, false, 'the write sees the account already removed');
  assert.deepEqual(secretPort.keys(), [], 'no Keychain item outlives its account');
});
