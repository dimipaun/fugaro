// The settings store's fences: their tests (Task P3.1; D111, D114, D42(2)).
//
// D111's negative half -- the two items are "never in a settings file" -- is
// only worth something if the settings file refuses them, so this file tests
// the three fences and, just as importantly, their POSITIVE CONTROLS:
//
//   * a legitimate installation tuple (D114) is accepted, including a long one
//     with dashes in it, which is what a project name looks like -- a fence
//     that refused ordinary names would be deleted by the next person who
//     tripped over it;
//   * a `Secret` is refused as a VALUE, whatever it holds, which is the fence
//     that does not depend on recognising any shape;
//   * a value that merely CARRIES a secret under a named field is refused,
//     because the walk is recursive;
//   * each credential shape this tree uses is refused by name, and the whole
//     list is the one exported as `secretShaped` -- the same list
//     `app-config.test.js` asks about `app.json`, so two files cannot disagree
//     about what a credential looks like.
//
// The credential-SHAPED literals below are built at run time from harmless
// pieces and are never written whole, which is `shared/presence/index.test.js`'s
// convention and the rule the brief states: no secret-shaped literal, even one
// that is plainly synthetic. Nothing here is a credential; every value is
// invented, and none is recorded from a real installation.

import assert from 'node:assert/strict';
import test from 'node:test';

import { isAccountId } from './account.js';
import {
  FORBIDDEN_KEY_SEGMENTS,
  MemorySettingsStore,
  SETTINGS_CODES,
  SettingsStore,
  isSettingsKey,
  secretShaped,
} from './settings.js';
import { SECRET_ITEMS, secret } from './secret.js';

const ID_TOKEN = 'synthetic-id-token';

/** A legitimate D114 tuple: the positive control the fences must not break. */
const TUPLE = Object.freeze({
  id: 'laptop',
  firebaseProjectId: 'my-very-long-firebase-project-name',
  authUid: 'uid-0001',
  fugaroProject: 'fugaro',
});

/** A credential-shaped string, assembled rather than written. */
function shaped(kind) {
  switch (kind) {
    case 'jwt':
      return ['eyJhbGciOiJub25lIn0', 'eyJzdWIiOiJ0ZXN0In0', 'c2lnbmF0dXJl'].join('.');
    case 'pem-private-key':
      return `-----BEGIN ${'PRIVATE'} KEY-----`;
    case 'google-api-key':
      return `AI${'za'}${'y'.repeat(35)}`;
    case 'model-api-key':
      return `s${'k-'}${'y'.repeat(20)}`;
    case 'long-hex':
      return 'a1b2c3d4'.repeat(5);
    default:
      throw new Error(`no such shape: ${kind}`);
  }
}

function makeStore() {
  const port = new MemorySettingsStore();
  return { port, store: new SettingsStore(port) };
}

test('a legitimate installation tuple round-trips, and nothing is renamed', async () => {
  const { port, store } = makeStore();
  assert.equal((await store.set('account.laptop', TUPLE)).ok, true);
  const read = await store.get('account.laptop');
  assert.equal(read.ok, true);
  assert.deepEqual(read.value, TUPLE);
  assert.deepEqual(port.keys(), [`fugaro.settings.account.laptop`]);
  const keys = await store.keys();
  assert.deepEqual([...keys.keys], ['account.laptop']);
});

test('the positive control: a long, dashed project name is NOT secret-shaped', () => {
  // A fence that refused this would refuse the owner's own installation.
  assert.equal(secretShaped(TUPLE.firebaseProjectId), null);
  assert.equal(secretShaped('fugaro'), null);
  assert.equal(secretShaped('uid-0001'), null);
  assert.equal(secretShaped(7), null);
});

test('every credential shape is refused, and named', async () => {
  const { store } = makeStore();
  for (const kind of ['jwt', 'pem-private-key', 'google-api-key', 'model-api-key', 'long-hex']) {
    const value = shaped(kind);
    assert.equal(secretShaped(value), kind, kind);
    const result = await store.set('account.laptop', { fugaroProject: value });
    assert.equal(result.code, SETTINGS_CODES.SECRET_VALUE, kind);
  }
});

test('a Secret is refused as a value, whatever its shape', async () => {
  const { port, store } = makeStore();
  const token = secret('idToken', ID_TOKEN).secret;
  const result = await store.set('account.laptop', token);
  assert.equal(result.code, SETTINGS_CODES.SECRET_VALUE);
  // ... and nested anywhere, including inside an array.
  assert.equal((await store.set('account.laptop', { deep: [1, { token }] })).code, SETTINGS_CODES.SECRET_VALUE);
  assert.equal((await store.set('account.laptop', [token])).code, SETTINGS_CODES.SECRET_VALUE);
  assert.deepEqual(port.keys(), [], 'a refused write writes nothing');
});

test('a value carrying a named secret field is refused, at any depth', async () => {
  const { store } = makeStore();
  for (const value of [
    { token: 'synthetic' },
    { nested: { privateKey: 'synthetic' } },
    { nested: [{ password: 'synthetic' }] },
    { idToken: 'synthetic' },
    { macKey: 'synthetic' },
  ]) {
    const result = await store.set('account.laptop', value);
    assert.equal(result.code, SETTINGS_CODES.SECRET_VALUE, JSON.stringify(value));
    assert.match(result.field, /^value/);
  }
});

test('a value that would not survive a JSON round trip is refused', async () => {
  const { store } = makeStore();
  for (const value of [undefined, () => {}, Symbol('x'), 1n, NaN, Infinity, new Map()]) {
    const result = await store.set('account.laptop', value);
    assert.equal(result.ok, false, String(value));
    assert.ok([SETTINGS_CODES.UNREADABLE_VALUE, SETTINGS_CODES.SECRET_VALUE].includes(result.code));
  }
});

test('a key that names an item, or the secure store\'s namespace, is refused', async () => {
  const { store } = makeStore();
  const cases = [
    ['fugaro.account.laptop.idToken', SETTINGS_CODES.RESERVED_KEY],
    ['token', SETTINGS_CODES.SECRET_KEY],
    ['ui.idToken', SETTINGS_CODES.SECRET_KEY],
    ['ui.macKey', SETTINGS_CODES.SECRET_KEY],
    ['password', SETTINGS_CODES.SECRET_KEY],
    ['ui.privateKey', SETTINGS_CODES.SECRET_KEY],
    ['secret', SETTINGS_CODES.SECRET_KEY],
    // An account key is `account.<id>` and nothing more: the id alphabet has
    // no `.`, so a smuggled name is a key this store does not own.
    ['account.laptop.token', SETTINGS_CODES.BAD_KEY],
    ['account.laptop.idToken', SETTINGS_CODES.BAD_KEY],
    ['account.', SETTINGS_CODES.BAD_KEY],
    ['', SETTINGS_CODES.BAD_KEY],
    ['has space', SETTINGS_CODES.BAD_KEY],
    ['a/b', SETTINGS_CODES.BAD_KEY],
    ['x'.repeat(129), SETTINGS_CODES.BAD_KEY],
    [7, SETTINGS_CODES.BAD_KEY],
  ];
  for (const [key, code] of cases) {
    const result = await store.set(key, 'synthetic');
    assert.equal(result.code, code, String(key));
    assert.equal(result.field, 'key');
  }
  assert.equal(isSettingsKey('account.laptop'), true);
  assert.equal(isSettingsKey('account.idToken'), true, 'the key rule is about NAMES, and isSettingsKey is only the alphabet');
});

test('the key fence covers D111\'s item names, derived and not repeated', async () => {
  const { store } = makeStore();
  // The property rather than the example: EVERY name in `SECRET_ITEMS`, as a
  // key or as a segment of one that is not an account key, is refused. The
  // list in `settings.js` is built from these names, so a third item added to
  // D111 is fenced the moment it is declared -- and this loop fails if that
  // derivation is ever dropped, which is the "one list of names, in one place"
  // rule.
  for (const item of SECRET_ITEMS) {
    for (const key of [`ui.${item}`, item]) {
      const result = await store.set(key, 'synthetic');
      assert.equal(result.code, SETTINGS_CODES.SECRET_KEY, key);
    }
  }
  assert.equal(SECRET_ITEMS.length, 2, 'D111: exactly two items');
});

test('every forbidden word is still a name the owner may give an installation', async () => {
  const { store } = makeStore();
  // Review finding: `#judgeKey` split the caller's key on `.` and refused any
  // segment in the word list, so the ACCOUNT STORE's own key -- `account.<id>`,
  // where the id is the owner's word -- was judged by it. An installation
  // called `token`, `secret`, `password`, `apiKey`, `idToken`, `macKey` and the
  // rest was refused with `secret-key`: the same fault as `mac`, fixed one word
  // short. This walks the whole list rather than the one word that was noticed,
  // so the next word added to the fence is covered by this test on the day it
  // is added.
  for (const word of [...FORBIDDEN_KEY_SEGMENTS, ...SECRET_ITEMS, 'mac', 'belong']) {
    assert.equal(isAccountId(word), true, `${word} is a legal account id`);
    // The account store's own key is accepted, whatever the word is ...
    assert.equal((await store.set(`account.${word}`, TUPLE)).ok, true, word);
    assert.equal((await store.get(`account.${word}`)).ok, true, word);
    // ... and a key that is NOT an account key still may not use it.
    if (FORBIDDEN_KEY_SEGMENTS.includes(word.toLowerCase())) {
      assert.equal((await store.set(word, TUPLE)).code, SETTINGS_CODES.SECRET_KEY, word);
    }
  }
});

test('a fence that refused the owner\'s own installation would be the wrong fence', async () => {
  const { port, store } = makeStore();
  // `mac` -- short for the machine the owner is most likely typing on -- is a
  // name a person picks for an account, and `account.mac` must therefore
  // round-trip. The word list refuses `macKey` and not `mac`, which is the
  // difference between a fence and a nuisance.
  assert.equal((await store.set('account.mac', TUPLE)).ok, true);
  assert.equal((await store.get('account.mac')).ok, true);
  assert.deepEqual(port.keys(), ['fugaro.settings.account.mac']);
});

test('an absent key is its own answer, and deleting one is not an error', async () => {
  const { store } = makeStore();
  assert.equal((await store.get('account.laptop')).code, SETTINGS_CODES.ABSENT);
  assert.equal((await store.delete('account.laptop')).ok, true);
});

test('a stored value this store could not have written is unreadable', async () => {
  const { port, store } = makeStore();
  // Planted through the port, which is what a future code path with a
  // different idea of "settings" would do.
  await port.setItem('fugaro.settings.account.laptop', JSON.stringify({ token: shaped('jwt') }));
  assert.equal((await store.get('account.laptop')).code, SETTINGS_CODES.UNREADABLE_VALUE);
  await port.setItem('fugaro.settings.account.laptop', 'not json at all');
  assert.equal((await store.get('account.laptop')).code, SETTINGS_CODES.UNREADABLE_VALUE);
});

test('a port that fails is a refusal, never a silent success', async () => {
  const failing = {
    async getItem() { throw new Error('disk'); },
    async setItem() { throw new Error('disk'); },
    async deleteItem() { throw new Error('disk'); },
    async keys() { throw new Error('disk'); },
  };
  const store = new SettingsStore(failing);
  assert.equal((await store.set('account.laptop', TUPLE)).code, SETTINGS_CODES.STORE_FAILED);
  assert.equal((await store.get('account.laptop')).code, SETTINGS_CODES.STORE_FAILED);
  assert.equal((await store.delete('account.laptop')).code, SETTINGS_CODES.STORE_FAILED);
  assert.equal((await store.keys()).code, SETTINGS_CODES.STORE_FAILED);
});

test('keys() reports this store\'s own keys and ignores a foreign one', async () => {
  const { port, store } = makeStore();
  await store.set('account.laptop', TUPLE);
  await port.setItem('someone-elses-key', 'synthetic');
  assert.deepEqual([...(await store.keys()).keys], ['account.laptop']);
});
