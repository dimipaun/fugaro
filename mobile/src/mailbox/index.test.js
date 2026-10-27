// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The mailbox module's one door: its test (mobile plan, Task P3.6).
//
// The surface is asserted as an exact set rather than "these names are
// present", for the reason `../../accounts/index.test.js` gives: a re-export
// added without a thought is how a key or a write path ends up reachable from a
// screen. Here the two things that must NOT be on the door are the tag
// functions (`mac.js`, `sha256.js`) and the ports. `mac.js` takes a key as an
// argument: a screen that could call it is a screen that could hold one, which
// is the sentence `SecretStore.use` exists to make unnecessary. And the real
// database adapter is built by `mobile/index.js`, the one file that knows what
// a device is, so that a screen cannot build a second write path.

import assert from 'node:assert/strict';
import test from 'node:test';

import * as mailbox from './index.js';
import { macOf } from './mac.js';

test('the door exports the client, the paths, the codes and the three fakes', () => {
  assert.deepEqual(Object.keys(mailbox).sort(), [
    'CLIENT_CODES',
    'EXPIRED',
    'GATE_CODES',
    'MAC_KEY_ITEM',
    'MAILBOX_ROOT',
    'MAILBOX_TTL_MS',
    'MemoryConnection',
    'MemoryMailboxStore',
    'MemorySpeaker',
    'OFFLINE_SPEECH',
    'PRESENCE_REASON',
    'REDACT_CODES',
    'SPEAK_CODES',
    'STORE_CODES',
    'createMailboxClient',
    'evaluateConnection',
    'evaluateGate',
    'isDatabaseKey',
    'mailboxPaths',
    'offlineSpeech',
    'outboundRedactor',
    'toPresenceHeartbeat',
  ]);
});

test('the tag functions and the adapters are not on the door', () => {
  // A tag function takes a key; the client is the only caller, and it takes one
  // inside `SecretStore.use`.
  for (const name of ['macOf', 'verifyMac', 'sha256', 'hmacSha256', 'canonicalBytes', 'toHex']) {
    assert.equal(name in mailbox, false, name);
  }
  // ... and no port: the client is handed one by the entry.
  for (const name of ['expoSecureStorePort', 'expoAsyncStoragePort', 'database']) {
    assert.equal(name in mailbox, false, name);
  }
});

test('the client the door builds is the one the tests in this directory build', async () => {
  // A door that exported a different builder would still pass the set above.
  const store = new mailbox.MemoryMailboxStore();
  const client = mailbox.createMailboxClient({
    account: {
      id: 'laptop',
      firebaseProjectId: 'fugaro-dev',
      authUid: 'uid-0001',
      fugaroProject: 'fugaro',
    },
    keys: { use: async (id, item, fn) => ({ ok: true, result: await fn('synthetic-key') }) },
    store,
    connection: new mailbox.MemoryConnection(),
    speaker: new mailbox.MemorySpeaker(),
    now: () => 1_700_000_000_000,
    newId: () => 'm-1',
    secrets: [],
    heartbeatIntervalMs: 60_000,
  });
  assert.deepEqual(client.paths.inbox, 'mailbox/uid-0001/inbox');
  // P6.10's gate fails closed on a consumer the phone has not observed (the
  // review's M2), so the door's client is put in the state the app is in: one
  // poll over the consumer's own heartbeat, signed the way the other end signs
  // it. Without it this test would be a test of `consumer-unobserved`.
  const beat = { at: 1_700_000_000_000 - 1_000, boot: 'boot-0001' };
  // `deliver`, not `write`: the heartbeat is the OTHER end's node, and
  // `store.js` says so where it defines the door ("`write` is the PHONE's door
  // ... a harness that wants the tree to already hold the other end's side is
  // not making a phone write and must not appear to be one"). Every other
  // fixture that plants a consumer's beat does it this way.
  assert.deepEqual(
    store.deliver('mailbox/uid-0001/heartbeat', {
      ...beat,
      mac: macOf(beat, 'synthetic-key', 'heartbeat'),
    }),
    { ok: true },
  );
  assert.equal((await client.poll()).presence.state, 'live');
  assert.deepEqual(await client.send('run 42 finished'), {
    ok: true,
    id: 'm-1',
    at: 1_700_000_000_000,
  });
});
