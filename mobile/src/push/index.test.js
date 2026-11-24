// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The push module's door (mobile plan, Task P5.2.8; D113, D259; the owner's Q7
// ruling of 2026-11-21).
//
// The exported surface is asserted as an **exact set**, because it is a
// decision: every name here is something a screen, `mobile/index.js` or a test
// may reach, and a name added without this list changing is a name nobody
// chose. Three names in it are the ones the plan is about -- `relayTokenSource`
// (the registration with the publisher's relay, and the token the mailbox
// carries), `deviceTokenSource` (the phone's own token, from the pinned SDK's
// default app, D259) and `createPush` (the switches and the registration over
// one account) -- and the rest is the vocabulary a screen renders: the phrases,
// the switch names, the state and code lists, and the memory doubles the tests
// build over.
//
// **The other half of the door's job is what is NOT in it.** No Firebase SDK
// import (the messaging namespace arrives as an object: the suite runs in a
// fresh clone with no `node_modules`, and `write-path.test.js` rule 5 keeps
// `@react-native-firebase/*` to `mobile/index.js`), and no store, key or path of
// its own -- `createPush` takes the mailbox client the app already builds, which
// is the app's ONE write path (D113). The scan below reads the directory for the
// first, and the wiring test reaches a real store through the door for the
// second: the write in it lands in the client's own store, and nothing else
// wrote anywhere.

import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { validateMessage } from '../../../shared/mailbox-schema/index.js';
import { MemorySecureStore, MemorySettingsStore, SecretStore, SettingsStore, secret } from '../accounts/index.js';
import { createMailboxClient } from '../mailbox/client.js';
import { MemoryConnection } from '../mailbox/connection.js';
import { MemoryMailboxStore } from '../mailbox/store.js';
import { MemorySpeaker } from '../mailbox/speaker.js';
import * as push from './index.js';
import { MemoryTokenSource } from './token.js';

/** The surface, sorted: the module's contract with the rest of the app. */
const SURFACE = Object.freeze([
  'ALERT_CATEGORIES',
  'ALERT_PHRASES',
  'ALERT_PUBLIC',
  'ALERT_VISIBILITY',
  'CATEGORY_FIELD',
  'CONTROL_CODES',
  'CONTROL_DEFAULTS',
  'CONTROL_LINES',
  'CONTROL_NAMES',
  'MESSAGE_CODES',
  'MemoryMailboxChange',
  'MemoryTokenSource',
  'PUSH_CHANNEL',
  'PUSH_LINES',
  'PUSH_SETTINGS_PREFIX',
  'PUSH_STATES',
  'PUSH_WAITING',
  'RECEIPT_CODES',
  'REGISTRATION_FIELDS',
  'REGISTRATION_KIND',
  'REGISTRATION_TEXT',
  'RELAY_CODES',
  'TOKEN_CODES',
  'WINDOW_FIELDS',
  'createPush',
  'createPushControls',
  'createPushReceipt',
  'createPushRegistration',
  'deviceTokenSource',
  'judgeField',
  'pushScreen',
  'pushSettingsKey',
  'pushStateLine',
  'readPushToken',
  'relayTokenSource',
]);

/** A mailbox id shaped like an Auth uid, and never a real one. */
const UID = 'AbCdEf0123456789ghIJkl';
const ACCOUNT = Object.freeze({
  id: 'laptop',
  firebaseProjectId: 'fugaro-dev',
  authUid: UID,
  fugaroProject: 'fugaro',
});
const MAC_KEY = 'synthetic-mac-key-0123456789abcdef';
const TOKEN = 'not-a-real-token-0123456789';

/** The ports the app builds anyway, plus a store to see what was written. */
async function ports() {
  const db = new MemoryMailboxStore();
  const keys = new SecretStore(new MemorySecureStore());
  const made = secret('macKey', MAC_KEY);
  assert.equal(made.ok, true, 'the fixture key is a valid secret');
  assert.equal((await keys.put(ACCOUNT.id, 'macKey', made.secret)).ok, true, 'the fixture key is stored');
  return {
    db,
    client: createMailboxClient({
      account: ACCOUNT,
      keys,
      store: db,
      connection: new MemoryConnection(),
      speaker: new MemorySpeaker(),
      now: () => 1_700_000_000_000,
      newId: (() => {
        let minted = 0;
        return () => `m-${++minted}`;
      })(),
      secrets: ['synthetic-token-0123456789'],
      heartbeatIntervalMs: 60_000,
    }),
    settings: new SettingsStore(new MemorySettingsStore()),
    token: new MemoryTokenSource({ token: TOKEN }),
  };
}

test('the door is exactly its names, and nothing else', () => {
  assert.deepEqual(Object.keys(push).sort(), [...SURFACE]);
  // The names the plan exists for are on it, and they are the ones a caller
  // reaches for: a surface that lost one would be a door to something else.
  assert.equal(typeof push.createPush, 'function');
  assert.equal(typeof push.relayTokenSource, 'function');
  assert.equal(typeof push.deviceTokenSource, 'function');
});

test('the door hands the SDK in as an object: no file in it imports a package', () => {
  // The fresh-clone rule (`mobile/README.md`) said as a trace of the directory:
  // every import in `src/push/` is a relative path, so nothing here needs a
  // `node_modules` to load -- and the Firebase SDK, which does, is passed in.
  // The comments come out first: these files are written in prose that quotes a
  // phrase, so a scan reading the raw text would flag the documentation rather
  // than an import (this file's own header does the same for `boundary.test.js`).
  const imports = (text) => text
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/(^|[^:])\/\/.*$/gm, '$1')
    .matchAll(/(?:\bfrom|\bimport)\s*\(?\s*['"]([^'"]+)['"]/g);
  // The control: the two spellings the app really uses are found, so a scan that
  // had stopped matching cannot pass on an empty directory.
  assert.deepEqual(
    [...imports("import a from './a.js';\nimport './b.js';")].map((match) => match[1]),
    ['./a.js', './b.js'],
  );
  const files = readdirSync(import.meta.dirname).filter((file) => file.endsWith('.js') && !file.endsWith('.test.js'));
  assert.equal(files.length > 5, true, 'the directory was read');
  for (const name of files) {
    const source = readFileSync(join(import.meta.dirname, name), 'utf8');
    for (const [, specifier] of imports(source)) {
      assert.equal(specifier.startsWith('.'), true, `${name} imports ${specifier}, and only a relative path can be loaded here`);
    }
  }
});

test('the door wires the two halves: a registration made through it lands in the client’s own store', async () => {
  const p = await ports();
  const door = push.createPush({
    client: p.client,
    settings: p.settings,
    accountId: ACCOUNT.id,
    token: p.token,
    platform: 'ios',
  });
  assert.equal(door.ok, true);
  // The key a screen shows is the LOGICAL one -- the settings store adds its own
  // `fugaro.settings.` prefix -- so it is the door's own function that composes
  // it, and not a string rebuilt here.
  assert.equal(door.key, push.pushSettingsKey(ACCOUNT.id));
  assert.equal(door.key, `${push.PUSH_SETTINGS_PREFIX}${ACCOUNT.id}`);
  assert.deepEqual(Object.keys(door).sort(), [
    'clearQuietHours', 'key', 'ok', 'prefs', 'register', 'reset', 'screen', 'setControl',
    'setQuietHours', 'state',
  ]);

  const outcome = await door.register();
  assert.equal(outcome.ok, true);
  assert.equal(outcome.state, push.PUSH_STATES.REGISTERED);
  // One write, in the mailbox client's store: the door builds no path, no key
  // and no store of its own (D113), and the record is one the consumer accepts.
  const writes = p.db.writes();
  assert.equal(writes.length, 1);
  assert.equal(writes[0].path, `mailbox/${UID}/inbox/${outcome.id}`);
  assert.equal(validateMessage((await p.db.read(writes[0].path)).value).ok, true);
});

test('a port that cannot be used is refused at construction, not at the first flip', async () => {
  const p = await ports();
  const whole = { client: p.client, settings: p.settings, accountId: ACCOUNT.id, token: p.token, platform: 'ios' };
  assert.throws(() => push.createPush(), TypeError, 'no options at all');
  assert.throws(() => push.createPush({}), TypeError, 'no ports at all');
  assert.throws(() => push.createPush({ ...whole, client: undefined }), TypeError);
  assert.throws(() => push.createPush({ ...whole, settings: undefined }), TypeError);
  assert.throws(() => push.createPush({ ...whole, accountId: '' }), TypeError);
  assert.throws(() => push.createPush({ ...whole, token: {} }), TypeError);
});
