// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The paired list: the phone's copy, the identity project's copy and the switch
// between them (task K3; D2796, F15, O16).
//
// The red half of K3 is here: **with the sync switch off nothing is sent.** The
// first test is that one -- a fetch that fails the test if it is reached at all
// -- because a list that leaves the phone when the project asked it not to is
// the whole of what the switch exists to prevent.

import assert from 'node:assert/strict';
import test from 'node:test';

import { SECRET_VALUE_MAX } from '../accounts/index.js';

import {
  PAIRED_LIST_MAX_ENTRIES,
  PAIRED_LIST_PREFIX,
  SYNC_CODES,
  PairedListStore,
  buildPairedListRead,
  buildPairedListWrite,
  mergeHeldNames,
  pairedListItem,
  parsePairedListResponse,
  readPairedList,
  recordPairing,
  upsertPairedEntry,
  wireEntries,
  writePairedList,
} from './sync.js';

const IDENTITY_ENDPOINT = 'https://identity-example.a.run.app';
const ID_TOKEN = 'not-a-credential-test-id-token';
const IDENTITY = 'google.1234567890';
const FINGERPRINT = 'Zm9vYmFyYmF6cXV1eC1ub3QtYS1jcmVkZW50aWFsLXRlc3Q';

const ENTRY = Object.freeze({
  project_id: 'fugaro-example',
  endpoint: 'https://mobile-example.a.run.app',
  identity: IDENTITY,
  consumer_key_fingerprint: FINGERPRINT,
  name: 'Example',
});

/** A keychain port that records what it was asked to write, and at which level. */
function port() {
  const items = new Map();
  const writes = [];
  return {
    writes,
    items,
    async getItem(key) {
      return items.has(key) ? items.get(key) : null;
    },
    async setItem(key, value, options) {
      writes.push({ key, options });
      items.set(key, value);
    },
    async deleteItem(key) {
      items.delete(key);
    },
  };
}

/** A fetch that answers one body and records every call it was given. */
function server({ status = 200, body } = {}) {
  const calls = [];
  const fetch = async (url, options = {}) => {
    calls.push({ url, method: options.method, headers: options.headers, body: options.body });
    const answer = typeof body === 'function' ? body(url, options, calls.length) : body;
    return { status: typeof status === 'function' ? status(calls.length) : status, text: async () => answer };
  };
  return { fetch, calls };
}

const listAnswer = (entries) => JSON.stringify({ entries });

test('with the sync switch off nothing is sent to the identity project', async () => {
  const store = new PairedListStore({ port: port() });
  let reached = 0;
  const fetch = async () => {
    reached += 1;
    throw new Error('the identity project must not be called while the project does not sync');
  };
  const recorded = await recordPairing({
    identityId: IDENTITY,
    entry: ENTRY,
    sync: false,
    store,
    identityEndpoint: IDENTITY_ENDPOINT,
    idToken: ID_TOKEN,
    fetch,
  });
  assert.equal(recorded.ok, true);
  assert.equal(recorded.sent, false);
  // The phone keeps its own list, and it is the one a new device may find in
  // the platform's backup -- which is the whole of what "with sync off it
  // restores from the synced keychain" means.
  const held = await store.read(IDENTITY);
  assert.deepEqual(held.entries, [ENTRY]);
  assert.equal(reached, 0);
});

test('the phone’s item is written at the level the switch names', async () => {
  const keychain = port();
  const store = new PairedListStore({ port: keychain });
  const { fetch } = server({ body: listAnswer([]) });
  await recordPairing({ identityId: IDENTITY, entry: ENTRY, sync: false, store, identityEndpoint: IDENTITY_ENDPOINT, idToken: ID_TOKEN, fetch });
  assert.deepEqual(keychain.writes.at(-1), { key: `${PAIRED_LIST_PREFIX}${IDENTITY}`, options: { synced: true } });
  await recordPairing({ identityId: IDENTITY, entry: { ...ENTRY, project_id: 'other' }, sync: true, store, identityEndpoint: IDENTITY_ENDPOINT, idToken: ID_TOKEN, fetch });
  // With sync on the identity project is the copy that has to outlive the
  // phone, so the phone's own item does not ride the platform's backup.
  assert.deepEqual(keychain.writes.at(-1), { key: `${PAIRED_LIST_PREFIX}${IDENTITY}`, options: { synced: false } });
});

test('a switch that is not a boolean sends nothing and writes nothing', async () => {
  for (const sync of [undefined, null, 'on', 1]) {
    const keychain = port();
    const store = new PairedListStore({ port: keychain });
    const reached = async () => assert.fail('nothing may be sent before the switch is known');
    const recorded = await recordPairing({ identityId: IDENTITY, entry: ENTRY, sync, store, identityEndpoint: IDENTITY_ENDPOINT, idToken: ID_TOKEN, fetch: reached });
    assert.equal(recorded.code, SYNC_CODES.BAD_SWITCH, String(sync));
    assert.deepEqual(keychain.writes, []);
  }
});

test('with sync on the entry reaches the identity project as C1’s four fields', async () => {
  const keychain = port();
  const store = new PairedListStore({ port: keychain });
  const other = { project_id: 'other-project', endpoint: 'https://other.a.run.app', identity: IDENTITY, consumer_key_fingerprint: 'A'.repeat(43) };
  const { fetch, calls } = server({ body: listAnswer([other]) });
  const recorded = await recordPairing({ identityId: IDENTITY, entry: ENTRY, sync: true, store, identityEndpoint: IDENTITY_ENDPOINT, idToken: ID_TOKEN, fetch });
  assert.equal(recorded.ok, true);
  assert.equal(recorded.sent, true);
  assert.equal(calls.length, 2);
  const [read, write] = calls;
  assert.equal(read.url, `${IDENTITY_ENDPOINT}/paired-list`);
  assert.equal(read.method, 'GET');
  assert.deepEqual(read.headers, { Authorization: `Bearer ${ID_TOKEN}` });
  assert.equal(read.body, undefined);
  assert.equal(write.method, 'PUT');
  assert.deepEqual(write.headers, { Authorization: `Bearer ${ID_TOKEN}`, 'Content-Type': 'application/json' });
  // The identity project may hold projects this phone has never seen, and a
  // write that dropped them would delete another device's pairings.
  assert.deepEqual(JSON.parse(write.body), {
    entries: [
      other,
      {
        project_id: ENTRY.project_id,
        endpoint: ENTRY.endpoint,
        identity: ENTRY.identity,
        consumer_key_fingerprint: ENTRY.consumer_key_fingerprint,
      },
    ],
  });
  // ... and no request body ever carries the readable name (D2796).
  assert.equal(write.body.includes('name'), false);
  assert.equal(write.body.includes('Example'), false);
});

test('one entry is C1’s shape or the refusal that names the member outside it', async () => {
  const cases = [
    [{ ...ENTRY, project_id: '' }, 'project_id'],
    [{ ...ENTRY, project_id: 'has space' }, 'project_id'],
    [{ ...ENTRY, project_id: 'x'.repeat(129) }, 'project_id'],
    [{ ...ENTRY, endpoint: 'http://mobile-example.a.run.app' }, 'endpoint'],
    [{ ...ENTRY, endpoint: 'https://' }, 'endpoint'],
    [{ ...ENTRY, identity: '' }, 'identity'],
    [{ ...ENTRY, identity: 'google/123' }, 'identity'],
    [{ ...ENTRY, consumer_key_fingerprint: '' }, 'consumer_key_fingerprint'],
    [{ ...ENTRY, consumer_key_fingerprint: 'x'.repeat(257) }, 'consumer_key_fingerprint'],
  ];
  for (const [entry, field] of cases) {
    const wire = wireEntries([entry]);
    assert.equal(wire.code, SYNC_CODES.BAD_ENTRY, JSON.stringify(entry));
    assert.equal(wire.field, field, JSON.stringify(entry));
  }
  // A member the contract does not name is refused rather than carried: a
  // field this release ignored is a field the next one reads as something else.
  assert.equal(wireEntries([{ ...ENTRY, workspace: 'acme' }]).field, 'workspace');
  assert.equal(wireEntries([{ ...ENTRY, name: 'n'.repeat(65) }]).field, 'name');
  assert.equal(wireEntries([{ ...ENTRY, name: 'not\nprintable' }]).field, 'name');
  assert.equal(wireEntries(ENTRY).field, 'entries');
  // One project, one entry: two would make the endpoint and the pinned key
  // depend on which was read first.
  assert.equal(wireEntries([ENTRY, { ...ENTRY, endpoint: 'https://other.a.run.app' }]).field, 'project_id');
  assert.equal(wireEntries(new Array(PAIRED_LIST_MAX_ENTRIES + 1).fill({ ...ENTRY, project_id: 'p' })).field, 'entries');
});

test('a write asks the identity project for its own list first, and sends C1’s four', () => {
  const built = buildPairedListWrite({ identityEndpoint: IDENTITY_ENDPOINT, idToken: ID_TOKEN, entries: [ENTRY] });
  assert.equal(built.ok, true);
  assert.deepEqual(JSON.parse(built.request.body), {
    entries: [
      {
        project_id: ENTRY.project_id,
        endpoint: ENTRY.endpoint,
        identity: ENTRY.identity,
        consumer_key_fingerprint: ENTRY.consumer_key_fingerprint,
      },
    ],
  });
  assert.equal(built.request.url, `${IDENTITY_ENDPOINT}/paired-list`);
  // The endpoint is where an identity's ID token is presented, so a cleartext
  // scheme is refused before the token leaves.
  assert.equal(buildPairedListWrite({ identityEndpoint: 'http://x.invalid', idToken: ID_TOKEN, entries: [ENTRY] }).code, SYNC_CODES.BAD_ENDPOINT);
  assert.equal(buildPairedListRead({ identityEndpoint: IDENTITY_ENDPOINT, idToken: '' }).code, SYNC_CODES.BAD_TOKEN);
});

test('an answer is the list this module reads, or the refusal that says why not', () => {
  const entries = [
    {
      project_id: ENTRY.project_id,
      endpoint: ENTRY.endpoint,
      identity: ENTRY.identity,
      consumer_key_fingerprint: ENTRY.consumer_key_fingerprint,
    },
  ];
  assert.deepEqual(parsePairedListResponse({ status: 200, body: listAnswer(entries) }).entries, entries);
  // An empty list is a list of no projects, and never an absence.
  assert.deepEqual(parsePairedListResponse({ status: 200, body: '{"entries":[]}' }).entries, []);
  assert.equal(parsePairedListResponse({ status: 200, body: '{}' }).field, 'entries');
  assert.equal(parsePairedListResponse({ status: 200, body: 'not json' }).code, SYNC_CODES.BAD_RESPONSE);
  assert.equal(parsePairedListResponse({ status: 200, body: '{"entries":[],"next":"x"}' }).field, 'next');
  // A body carrying a name is not a body this service sends, and reading it
  // leniently would put a field the phone never sent into its own list.
  assert.equal(parsePairedListResponse({ status: 200, body: listAnswer([{ ...entries[0], name: 'Example' }]) }).code, SYNC_CODES.BAD_RESPONSE);
  assert.equal(parsePairedListResponse({ status: 200, body: listAnswer([{ ...entries[0], endpoint: 'http://x.invalid' }]) }).code, SYNC_CODES.BAD_RESPONSE);
  // K1's own refusals, carried through as themselves: a code the app did not
  // know would be a refusal shown as a crash.
  assert.equal(parsePairedListResponse({ status: 503, body: '{"error":{"code":"paired_list_unavailable"}}' }).code, 'paired_list_unavailable');
  assert.equal(parsePairedListResponse({ status: 401, body: '{"error":{"code":"unverified_id_token"}}' }).code, 'unverified_id_token');
  assert.equal(parsePairedListResponse({ status: 500, body: '{"error":{"code":"who_knows"}}' }).code, SYNC_CODES.UNKNOWN_CODE);
  assert.equal(parsePairedListResponse({ status: 404, body: '' }).code, SYNC_CODES.UNKNOWN_CODE);
});

test('a read of the identity project’s list is one GET with the caller’s ID token', async () => {
  const { fetch, calls } = server({ body: listAnswer([]) });
  const read = await readPairedList({ identityEndpoint: IDENTITY_ENDPOINT, idToken: ID_TOKEN, fetch });
  assert.equal(read.ok, true);
  assert.equal(calls[0].url, `${IDENTITY_ENDPOINT}/paired-list`);
  assert.equal(calls[0].method, 'GET');
  const failed = await writePairedList({ identityEndpoint: IDENTITY_ENDPOINT, idToken: ID_TOKEN, entries: [ENTRY], fetch: async () => { throw new Error('offline'); } });
  assert.equal(failed.code, SYNC_CODES.NETWORK);
});

test('the phone’s item round-trips, and a value it could not have written is refused', async () => {
  const keychain = port();
  const store = new PairedListStore({ port: keychain });
  assert.deepEqual((await store.read(IDENTITY)).entries, []);
  assert.equal((await store.write(IDENTITY, [ENTRY])).ok, true);
  assert.deepEqual((await store.read(IDENTITY)).entries, [ENTRY]);
  assert.equal(await keychain.getItem(pairedListItem(IDENTITY)) !== null, true);

  // A document with a member this version does not write, a version from
  // nowhere, and an entry outside the shape are each unreadable rather than
  // repaired: "nearly the right list" restores a project at the wrong endpoint.
  await keychain.setItem(pairedListItem(IDENTITY), JSON.stringify({ v: 1, entries: [ENTRY], extra: true }));
  assert.equal((await store.read(IDENTITY)).code, SYNC_CODES.UNREADABLE);
  await keychain.setItem(pairedListItem(IDENTITY), JSON.stringify({ v: 2, entries: [] }));
  assert.equal((await store.read(IDENTITY)).code, SYNC_CODES.UNREADABLE);
  await keychain.setItem(pairedListItem(IDENTITY), JSON.stringify({ v: 1, entries: [{ ...ENTRY, endpoint: 'http://x.invalid' }] }));
  assert.equal((await store.read(IDENTITY)).code, SYNC_CODES.UNREADABLE);
  await keychain.setItem(pairedListItem(IDENTITY), 'not json');
  assert.equal((await store.read(IDENTITY)).code, SYNC_CODES.UNREADABLE);

  assert.equal((await store.read('not an identity')).code, SYNC_CODES.BAD_IDENTITY);
  assert.equal((await store.write('', [])).code, SYNC_CODES.BAD_IDENTITY);
  assert.equal((await store.write(IDENTITY, [ENTRY, ENTRY])).field, 'project_id');
  assert.equal((await store.delete(IDENTITY)).ok, true);
  assert.deepEqual((await store.read(IDENTITY)).entries, []);
});

test('a list the platform cannot hold is refused rather than written', async () => {
  const keychain = port();
  const store = new PairedListStore({ port: keychain });
  const big = Array.from({ length: 40 }, (unused, index) => ({
    ...ENTRY,
    project_id: `project-${index}`,
    name: 'n'.repeat(64),
  }));
  const written = await store.write(IDENTITY, big);
  assert.equal(written.code, SYNC_CODES.LIST_TOO_BIG);
  assert.deepEqual(keychain.writes, []);
  assert.ok(JSON.stringify(big).length > SECRET_VALUE_MAX);
  // ... and a list the platform does hold is written.
  assert.equal((await store.write(IDENTITY, [ENTRY])).ok, true);
});

test('a value the platform refuses is a refusal and never a stored list', async () => {
  const locked = async () => {
    throw new Error('locked');
  };
  const store = new PairedListStore({ port: { getItem: locked, setItem: locked, deleteItem: locked } });
  assert.equal((await store.write(IDENTITY, [ENTRY])).code, SYNC_CODES.STORE_FAILED);
  assert.equal((await store.read(IDENTITY)).code, SYNC_CODES.STORE_FAILED);
  assert.equal((await store.delete(IDENTITY)).code, SYNC_CODES.STORE_FAILED);
});

test('an entry replaces the one for its project and keeps the order of the rest', () => {
  const first = { ...ENTRY, name: 'One' };
  const second = { ...ENTRY, project_id: 'two', name: 'Two' };
  assert.deepEqual(upsertPairedEntry([first, second], { ...first, name: 'Renamed' }), [{ ...second }, { ...first, name: 'Renamed' }]);
  assert.deepEqual(upsertPairedEntry([], first), [first]);
});

test('the names a phone already had survive a list that came back without them', () => {
  const wire = { project_id: ENTRY.project_id, endpoint: ENTRY.endpoint, identity: ENTRY.identity, consumer_key_fingerprint: ENTRY.consumer_key_fingerprint };
  assert.deepEqual(mergeHeldNames([wire], [ENTRY]), [{ ...wire, name: 'Example' }]);
  assert.deepEqual(mergeHeldNames([wire], []), [{ ...wire, name: '' }]);
  assert.deepEqual(mergeHeldNames([wire], [{ ...ENTRY, project_id: 'gone', name: 'Gone' }]), [{ ...wire, name: '' }]);
});
