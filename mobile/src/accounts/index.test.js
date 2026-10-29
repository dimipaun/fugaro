// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The account module's one door: its test (mobile plan, Task P3.1).
//
// The surface is asserted as an exact set rather than "these names are
// present". A re-export added without a thought is how a port, a raw store or
// a helper that hands back material ends up reachable from a screen -- and the
// boundary scan's allow-lists are written against the files, so the door and
// the scan have to agree about what outside code can even name.

import assert from 'node:assert/strict';
import test from 'node:test';

import * as accounts from './index.js';

test('the door exports the store, the tuple and the two items, and no ports', () => {
  assert.deepEqual(Object.keys(accounts).sort(), [
    'ACCOUNT_CODES',
    'ACCOUNT_FIELDS',
    'ACCOUNT_ID_MAX',
    'ACCOUNT_KEY_PREFIX',
    'ACCOUNT_TEXT_MAX',
    'AccountCache',
    'AccountStore',
    'CACHE_PREFIX',
    'MemorySecureStore',
    'MemorySettingsStore',
    'RESOURCE_CODES',
    'ResourceRegistry',
    'SECRET_CODES',
    'SECRET_ITEMS',
    'SECRET_KEY_PREFIX',
    'SECRET_VALUE_MAX',
    'SECURE_STORE_CODES',
    'SETTINGS_CODES',
    'SETTINGS_KEY_MAX',
    'SETTINGS_KEY_PREFIX',
    'STORE_CODES',
    'Secret',
    'SecretStore',
    'SettingsStore',
    'cacheNamespace',
    'isAccountId',
    'isSecret',
    'isSecretItem',
    'isSettingsKey',
    'sameInstallation',
    'secret',
    'secretKey',
    'secretShaped',
    'validateAccount',
  ]);
});

test('the device adapters are not on the door: only the entry builds a port', () => {
  // A screen that could import `expoSecureStorePort` could build a second
  // secret store over the same Keychain -- D114's "no shared token store"
  // undone by an import. The entry (`mobile/index.js`) is the one file that
  // knows what a device is.
  assert.equal('expoSecureStorePort' in accounts, false);
  assert.equal('asyncStoragePort' in accounts, false);
});

test('the store is the only thing that returns material, and it needs both names', async () => {
  const store = new accounts.AccountStore({
    secrets: new accounts.SecretStore(new accounts.MemorySecureStore()),
    settings: new accounts.SettingsStore(new accounts.MemorySettingsStore()),
    registry: new accounts.ResourceRegistry(),
  });
  await store.add({ id: 'laptop', firebaseProjectId: 'fugaro-dev', authUid: 'uid-0001', fugaroProject: 'fugaro' });
  await store.setSecret('laptop', 'idToken', accounts.secret('idToken', 'synthetic-token').secret);
  // One account and one item: there is no call that returns "the credentials".
  assert.equal((await store.secret('laptop', 'idToken')).secret.reveal(), 'synthetic-token');
  assert.equal((await store.secret('laptop', 'macKey')).code, 'absent');
  // ... and the list carries none of it.
  assert.equal(JSON.stringify(await store.list()).includes('synthetic-token'), false);
});
