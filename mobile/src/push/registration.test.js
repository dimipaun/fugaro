// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The registration, end to end through the app's one write path: its tests
// (mobile plan, Task P5.2.8; D113, D166; the design's §3.5 (b) and §6).
//
// **This file carries the plan's verification sentences**, so they are named
// here rather than left for a reader to infer:
//
//   1. *the registration message validates against the schema and carries the
//      token* -- asserted on the record the DATABASE holds, with
//      `validateMessage`, and on the tag with `verifyMac`;
//   2. *a token that cannot be obtained is not written as an empty field -- the
//      write is skipped and the UI says so* -- the store's write log is empty,
//      no inbox node exists, and `pushScreen` renders the phrase;
//   3. *the switches travel in the same message* -- the flip is in the same
//      record as the token, not in a second message or a second path;
//   4. *`write-path.test.js` still holds* -- asserted by that scan, which reads
//      the tree: this file writes nothing, and the client does (D113);
//   5. *the app names the Firebase app instance it asks for a token on* --
//      `messaging.test.js`.
//
// **No token-shaped literal is written here either.** The token below is
// obviously synthetic, and the one test that asks about a token the schema
// refuses repeats a character rather than writing a credential.

import assert from 'node:assert/strict';
import test from 'node:test';

import { validateMessage } from '../../../shared/mailbox-schema/index.js';
import { MemorySecureStore, MemorySettingsStore, SecretStore, SettingsStore, secret } from '../accounts/index.js';
import { createMailboxClient } from '../mailbox/client.js';
import { MemoryConnection } from '../mailbox/connection.js';
import { verifyMac } from '../mailbox/mac.js';
import { MemoryMailboxStore } from '../mailbox/store.js';
import { MemorySpeaker } from '../mailbox/speaker.js';
import { CONTROL_DEFAULTS, createPushControls } from './controls.js';
import { REGISTRATION_KIND, REGISTRATION_TEXT } from './message.js';
import { PUSH_STATES, createPushRegistration } from './registration.js';
import { pushStateLine } from './screen.js';
import { MemoryTokenSource, TOKEN_CODES } from './token.js';

/** A mailbox id shaped like an Auth uid, and never a real one. */
const UID = 'AbCdEf0123456789ghIJkl';
const ACCOUNT = Object.freeze({
  id: 'laptop',
  firebaseProjectId: 'fugaro-dev',
  authUid: UID,
  fugaroProject: 'fugaro',
});
/** D171's per-pairing key, synthetic: the store's own shape, and no real key. */
const MAC_KEY = 'synthetic-mac-key-0123456789abcdef';
const SECRETS = ['synthetic-token-0123456789'];
const TOKEN = 'not-a-real-token-0123456789';
const T0 = 1_700_000_000_000;
const ZONE = 'Europe/Berlin';

/**
 * A paired phone with a token source and §3.8's switches, over the app's own
 * ports -- the mailbox client, the settings store and the memory doubles.
 *
 * **The client is built with no licence port**, which is the app's state today
 * (D523: nothing wires a verifier yet, so `UNENFORCED_LICENCE` is the default
 * and has no reading to make), and **no poll has run**: a registration is not
 * addressed to the consumer's presence, and this harness would let a client
 * that asked for it fail loudly (`consumer-unobserved`).
 */
async function harness(options = {}) {
  const settingsPort = new MemorySettingsStore();
  const settings = new SettingsStore(settingsPort);
  const db = new MemoryMailboxStore();
  const keys = new SecretStore(new MemorySecureStore());
  const made = secret('macKey', MAC_KEY);
  assert.equal(made.ok, true, 'the fixture key is a valid secret');
  assert.equal((await keys.put(ACCOUNT.id, 'macKey', made.secret)).ok, true, 'the fixture key is stored');

  const connection = options.connection ?? new MemoryConnection();
  const client = createMailboxClient({
    account: ACCOUNT,
    keys,
    store: db,
    connection,
    speaker: new MemorySpeaker(),
    now: () => T0,
    newId: (() => {
      let minted = 0;
      return () => `m-${++minted}`;
    })(),
    secrets: SECRETS,
    heartbeatIntervalMs: 60_000,
  });
  const controls = createPushControls({
    settings,
    accountId: ACCOUNT.id,
    ...(options.zone === undefined ? {} : { zone: options.zone ?? (() => ZONE) }),
  });
  const token = new MemoryTokenSource(
    options.token === undefined && options.fail === undefined
      ? { token: TOKEN }
      : { token: options.token ?? null, fail: options.fail === true },
  );
  const registration = createPushRegistration({
    client,
    controls,
    token,
    platform: options.platform ?? 'ios',
  });
  const inbox = `mailbox/${UID}/inbox`;
  return {
    client,
    registration,
    controls,
    settings,
    settingsPort,
    db,
    keys,
    token,
    connection,
    /** The record the database holds for `id`, or `null`. */
    async record(id) {
      const read = await db.read(`${inbox}/${id}`);
      return read.ok ? read.value : null;
    },
    /** Every inbox node the database holds, as a list of records. */
    async records() {
      const read = await db.read(inbox);
      return read.ok && read.value !== null ? Object.values(read.value) : [];
    },
  };
}

test('the registration validates against the schema, carries the token, and is signed for this pairing', async () => {
  const h = await harness();
  const outcome = await h.registration.register();
  assert.equal(outcome.ok, true);
  assert.equal(outcome.state, PUSH_STATES.REGISTERED);
  assert.equal(typeof outcome.id, 'string');
  assert.equal(outcome.at, T0);

  // Exactly one node, in this mailbox's inbox and nowhere else.
  const writes = h.db.writes();
  assert.equal(writes.length, 1, 'one registration is one write');
  assert.equal(writes[0].path, `mailbox/${UID}/inbox/${outcome.id}`);

  const record = await h.record(outcome.id);
  const valid = validateMessage(record);
  assert.equal(valid.ok, true, 'the message the app wrote is one the consumer accepts');
  assert.equal(record.kind, REGISTRATION_KIND);
  assert.equal(record.token, TOKEN, 'the token travels in the field the kind requires');
  assert.equal(record.platform, 'ios');
  assert.equal(record.sender, UID, 'P2.1’s template holds both families to sender == $mb');
  assert.equal(record.text, REGISTRATION_TEXT);
  // P6.3: the tag covers the record's fields, and it is this pairing's key that
  // made it -- the consumer's own check, run here on the bytes the app wrote.
  assert.equal(verifyMac(record, MAC_KEY, 'inbox'), true);
  assert.equal(verifyMac(record, 'synthetic-mac-key-fedcba9876543210', 'inbox'), false, 'the control');
  // §3.5 (b): the prefs ride in the SAME message. A registration with no prefs
  // is legal for the schema and useless for §3.8, so its presence is asserted.
  assert.deepEqual({ ...record.prefs }, { ...CONTROL_DEFAULTS });
});

test('a token that cannot be obtained writes nothing at all, and the app says so', async () => {
  // Two ways of having no token, and one broken source: none of them writes.
  for (const [what, options] of [
    ['no token', { token: null }],
    ['a source that fails', { fail: true }],
  ]) {
    const h = await harness(options);
    const outcome = await h.registration.register();
    assert.equal(outcome.ok, false, what);
    assert.equal(outcome.state, PUSH_STATES.UNREGISTERED, what);
    assert.equal(
      outcome.code,
      options.fail === true ? TOKEN_CODES.TOKEN_FAILED : TOKEN_CODES.NO_TOKEN,
      what,
    );
    assert.equal(h.db.writes().length, 0, `${what}: the store was not asked to write`);
    assert.deepEqual(await h.records(), [], `${what}: no inbox node exists`);
    assert.equal(outcome.id, null, `${what}: no id was minted`);
    // ... and the UI says so: a phrase, and never the token or a false success.
    const line = pushStateLine(outcome);
    assert.equal(typeof line, 'string');
    assert.notEqual(line, '', what);
    assert.equal(line.includes(TOKEN), false, 'no phrase carries a token');
    assert.equal(line, pushStateLine(h.registration.state()), 'the screen renders the last outcome');
  }
});

test('a token the schema would refuse is refused before anything is signed', async () => {
  const h = await harness({ token: 't'.repeat(257) });
  const outcome = await h.registration.register();
  assert.equal(outcome.ok, false);
  assert.equal(outcome.code, 'too-long');
  assert.equal(outcome.field, 'token');
  assert.equal(h.db.writes().length, 0);
});

test('the switches travel in the same message as the token', async () => {
  const h = await harness({ zone: () => ZONE });
  await h.controls.setControl('cap_near', true);
  await h.controls.setControl('prompt', false);
  await h.controls.setQuietHours({ start: 1320, end: 420 });
  const outcome = await h.registration.register();
  assert.equal(outcome.ok, true);
  const record = await h.record(outcome.id);
  assert.deepEqual({ ...record.prefs }, {
    ...CONTROL_DEFAULTS,
    cap_near: true,
    prompt: false,
    quiet_hours: { zone: ZONE, start: 1320, end: 420 },
  });
  // One message, not two: the token and the switches are the same record.
  assert.equal(h.db.writes().length, 1);
  assert.equal(record.token, TOKEN);
  assert.equal(validateMessage(record).ok, true);
});

test('the switches the owner set are read at the moment of the write', async () => {
  const h = await harness();
  await h.registration.register();
  await h.controls.setControl('kill_switch', false);
  const second = await h.registration.register();
  const record = await h.record(second.id);
  assert.equal(record.prefs.kill_switch, false, 'the second registration carries the new switch');
  assert.equal(h.db.writes().length, 2, 'a switch flip registers again: §3.8 is honoured server-side');
  assert.deepEqual(
    h.db.writes().map((write) => write.path),
    [`mailbox/${UID}/inbox/m-1`, `mailbox/${UID}/inbox/m-2`],
    'a fresh id each time: a registration is never re-minted, and never suppressed',
  );
});

test('a registration needs no poll and no orchestrator: presence is not asked', async () => {
  // The client was built with no poll at all, so P6.10's gate would refuse
  // `consumer-unobserved` -- push exists for the times the consumer is away
  // (§1), so a phone whose orchestrator has never been seen registers anyway.
  const h = await harness();
  assert.equal((await h.registration.register()).ok, true);
});

test('a phone with no connection registers nothing, and retries on the next call', async () => {
  const connection = new MemoryConnection({ online: false });
  const h = await harness({ connection });
  const outcome = await h.registration.register();
  assert.equal(outcome.ok, false);
  assert.equal(outcome.state, PUSH_STATES.UNREGISTERED);
  assert.equal(outcome.code, 'no-connection');
  assert.equal(h.db.writes().length, 0, 'nothing is handed to the SDK while offline (D167)');
  // The retry is the next call (§3.5 (b): "on next open"), and nothing is kept
  // anywhere in between: no outbox entry, nothing pending.
  assert.deepEqual([...h.client.pending()], []);
  connection.setOnline(true);
  const again = await h.registration.register();
  assert.equal(again.ok, true);
  assert.equal(again.state, PUSH_STATES.REGISTERED);
});

test('a record the app cannot read is not a registration without switches', async () => {
  const h = await harness();
  h.settingsPort.setItem(`fugaro.settings.push-${ACCOUNT.id}`, 'true');
  const outcome = await h.registration.register();
  assert.equal(outcome.ok, false);
  assert.equal(outcome.state, PUSH_STATES.UNREGISTERED);
  assert.equal(h.db.writes().length, 0, 'a message that could not say what the switches are is not sent');
  assert.equal(pushStateLine(outcome), pushStateLine(h.registration.state()));
});

test('a platform the schema does not name registers nothing, and says which field', async () => {
  const h = await harness({ platform: 'web' });
  const outcome = await h.registration.register();
  assert.equal(outcome.ok, false);
  assert.equal(outcome.code, 'unknown-platform');
  assert.equal(outcome.field, 'platform');
  assert.equal(h.db.writes().length, 0);
  // Judged once, at wiring time: no call can change the phone's platform, so
  // every call answers the same way, and nothing is retried for ever.
  assert.deepEqual({ ...(await h.registration.register()) }, { ...outcome });
});

test('the state is what happened, and "never" is not "unregistered"', async () => {
  const h = await harness();
  const before = h.registration.state();
  assert.equal(before.ok, false);
  assert.equal(before.state, PUSH_STATES.NEVER);
  assert.equal(before.id, null);
  assert.equal(pushStateLine(before), pushStateLine({ state: PUSH_STATES.NEVER }));
  h.token.set(null);
  const refused = await h.registration.register();
  assert.equal(refused.state, PUSH_STATES.UNREGISTERED, 'a refused call is not "nothing has been asked"');
  h.token.set(TOKEN);
  h.token.set('not-a-real-token-rotated');
  const registered = await h.registration.register();
  assert.equal(registered.state, PUSH_STATES.REGISTERED);
  assert.equal((await h.record(registered.id)).token, 'not-a-real-token-rotated');
  // Every outcome is frozen: a screen renders it and never edits it.
  for (const outcome of [before, refused, registered]) assert.equal(Object.isFrozen(outcome), true);
});

test('the three ports are required, and half of one is a wiring bug', async () => {
  const h = await harness();
  const client = h.client;
  const controls = h.controls;
  const token = { token: async () => ({ ok: true, token: TOKEN }) };
  assert.throws(() => createPushRegistration({ controls, token, platform: 'ios' }), TypeError);
  assert.throws(() => createPushRegistration({ client: {}, controls, token, platform: 'ios' }), TypeError);
  assert.throws(() => createPushRegistration({ client, token, platform: 'ios' }), TypeError);
  assert.throws(() => createPushRegistration({ client, controls: {}, token, platform: 'ios' }), TypeError);
  assert.throws(() => createPushRegistration({ client, controls, platform: 'ios' }), TypeError);
  assert.throws(() => createPushRegistration({ client, controls, token: {}, platform: 'ios' }), TypeError);
});
