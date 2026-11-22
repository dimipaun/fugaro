// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The two halves over one account: its tests (mobile plan, Task P5.2.8; D113,
// D114, D123, D160; the design's §3.5 (b) and §3.8).
//
// **The one property this file exists for: a switch is not saved, it is
// PUBLISHED.** §3.8's switches are honoured server-side, and the app cannot
// write the push node (D166) -- so the only way a switch means anything is the
// registration message that carries it, and the door answers the two outcomes
// separately rather than folding them into one boolean. Both halves are asserted
// below: a flip that is stored and published (the record the database holds
// carries the switch), a flip that is stored and NOT published (the phone keeps
// it, says so, and the next open sends it), and a flip that could not be stored
// at all (nothing is published, because there is no record to carry).
//
// **Two accounts are two records.** The store is the one the app builds for
// every account (D114: a separate entry per account), so the same settings
// store holds `push-<id>` per account and one account's flip is not another's --
// the last section.
//
// The client is the app's OWN (`createMailboxClient`), not a stub: what a flip
// publishes is a signed message in an inbox, and a test that took the client's
// word for it would not be testing the path D113 is about.

import assert from 'node:assert/strict';
import test from 'node:test';

import { validateMessage } from '../../../shared/mailbox-schema/index.js';
import { MemorySecureStore, MemorySettingsStore, SecretStore, SettingsStore, secret } from '../accounts/index.js';
import { createMailboxClient } from '../mailbox/client.js';
import { MemoryConnection } from '../mailbox/connection.js';
import { verifyMac } from '../mailbox/mac.js';
import { MemoryMailboxStore } from '../mailbox/store.js';
import { MemorySpeaker } from '../mailbox/speaker.js';
import { CONTROL_DEFAULTS } from './controls.js';
import { createPush } from './push.js';
import { PUSH_STATES } from './registration.js';
import { PUSH_WAITING } from './screen.js';
import { TOKEN_CODES, MemoryTokenSource } from './token.js';

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
const ZONE = 'Europe/Berlin';

/**
 * One account's world: the mailbox client over memory ports, the app's settings
 * store, a token source and a connection that can be turned off.
 *
 * One store per *test*, so the two doors in the last test really do share it.
 */
async function world(options = {}) {
  const settingsPort = new MemorySettingsStore();
  const settings = new SettingsStore(settingsPort);
  const db = new MemoryMailboxStore();
  const keys = new SecretStore(new MemorySecureStore());
  const made = secret('macKey', MAC_KEY);
  assert.equal(made.ok, true, 'the fixture key is a valid secret');
  assert.equal((await keys.put(ACCOUNT.id, 'macKey', made.secret)).ok, true, 'the fixture key is stored');

  const connection = options.connection ?? new MemoryConnection();
  let minted = 0;
  const client = createMailboxClient({
    account: ACCOUNT,
    keys,
    store: db,
    connection,
    speaker: new MemorySpeaker(),
    // A clock the overlapping-flips test drives: the consumer applies
    // registrations in `at` order, so a test about which WRITE wins has to be
    // able to tell the two apart.
    now: options.now ?? (() => 1_700_000_000_000),
    newId: () => `m-${++minted}`,
    secrets: ['synthetic-token-0123456789'],
    heartbeatIntervalMs: 60_000,
  });
  const token = options.token ?? new MemoryTokenSource({ token: TOKEN });
  const door = createPush({
    client,
    settings,
    accountId: ACCOUNT.id,
    ...(options.zone === undefined ? {} : { zone: options.zone }),
    token,
    platform: options.platform ?? 'ios',
  });
  return {
    door,
    client,
    settings,
    settingsPort,
    db,
    connection,
    token,
    /** Every node the inbox holds, newest last. */
    async inbox() {
      const read = await db.read(`mailbox/${UID}/inbox`);
      return read.ok && read.value !== null ? Object.values(read.value) : [];
    },
  };
}

test('a flip is stored and published: the record the inbox holds carries the switch', async () => {
  const w = await world({ zone: () => ZONE });
  const answer = await w.door.setControl('cap_near', true);
  // Both halves, and separately: the switch was STORED (`ok`) and the message
  // that carries it LANDED (`published.ok`) -- §3.8 is honoured by the sender,
  // so a switch that never left the phone silences nothing.
  assert.equal(answer.ok, true);
  assert.equal(answer.prefs.cap_near, true);
  assert.equal(answer.published.ok, true);
  assert.equal(answer.published.state, PUSH_STATES.REGISTERED);

  const written = await w.inbox();
  assert.equal(written.length, 1, 'one flip is one registration, and not a second message');
  const record = written[0];
  assert.equal(record.kind, 'registration');
  assert.equal(record.token, TOKEN);
  assert.equal(validateMessage(record).ok, true);
  assert.equal(record.prefs.cap_near, true, 'the flip travelled in the same message as the token');
  assert.equal(record.prefs.kill_switch, CONTROL_DEFAULTS.kill_switch, 'and the switches the owner did not touch are in it too');
  assert.deepEqual({ ...w.door.state() }, { ...answer.published }, 'the door’s state is the last registration');
});

test('a flip that is stored and not published reports both, and the next call sends it', async () => {
  const connection = new MemoryConnection({ online: false });
  const w = await world({ connection, zone: () => ZONE });
  const answer = await w.door.setControl('prompt', false);
  // Stored, so the owner's choice is not lost; not published, so a screen says
  // "saved here, not sent yet" instead of claiming a silence nobody heard.
  assert.equal(answer.ok, true);
  assert.equal(answer.prefs.prompt, false);
  assert.equal(answer.published.ok, false);
  assert.equal(answer.published.code, 'no-connection');
  assert.equal(answer.published.state, PUSH_STATES.UNREGISTERED);
  assert.deepEqual(await w.inbox(), [], 'nothing is handed to the SDK while offline (D167)');
  assert.deepEqual([...w.client.pending()], [], 'and nothing is queued: the retry is the next call');

  // The switches are held where a screen reads them, so the phone did not lose
  // the flip while it could not send it.
  const prefs = await w.door.prefs();
  assert.equal(prefs.ok, true);
  assert.equal(prefs.prefs.prompt, false);

  connection.setOnline(true);
  const again = await w.door.register();
  assert.equal(again.ok, true);
  assert.equal((await w.inbox())[0].prefs.prompt, false, 'the retry carries the flip that was waiting');
});

test('a flip that cannot be stored publishes nothing: there is no record to carry', async () => {
  const w = await world();
  // A record this build cannot read is a refusal rather than a silent set of
  // defaults (`controls.js`), and a refusal is not a change to publish. It is
  // planted through the raw port, because the store's own `set` would not write
  // a value it would refuse to read back.
  await w.settingsPort.setItem(SettingsStore.rawKey(w.door.key), 'true');
  const answer = await w.door.setControl('prompt', false);
  assert.equal(answer.ok, false);
  assert.equal(answer.published, undefined, 'a store refusal answers as itself and never as a published change');
  assert.deepEqual(await w.inbox(), []);
  assert.equal(w.door.state().state, PUSH_STATES.NEVER, 'nothing was asked of the registration at all');

  // ... and the repair puts the defaults back and publishes them in one act.
  const repaired = await w.door.reset();
  assert.equal(repaired.ok, true);
  assert.equal(repaired.published.ok, true);
  assert.deepEqual({ ...(await w.inbox())[0].prefs }, { ...CONTROL_DEFAULTS });
});

test('a switch the schema does not have is refused with the schema’s own code, and nothing is sent', async () => {
  const w = await world();
  const answer = await w.door.setControl('cap_reached_soon', true);
  assert.equal(answer.ok, false);
  assert.equal(answer.code, 'unknown-field');
  assert.equal(answer.field, 'prefs.cap_reached_soon');
  assert.deepEqual(await w.inbox(), []);
});

test('quiet hours travel as a window with the zone NAME, and clearing them publishes that too', async () => {
  const w = await world({ zone: () => ZONE });
  const on = await w.door.setQuietHours({ start: 1320, end: 420 });
  assert.equal(on.ok, true);
  assert.equal(on.published.ok, true);
  const record = (await w.inbox())[0];
  assert.deepEqual({ ...record.prefs.quiet_hours }, { zone: ZONE, start: 1320, end: 420 });
  // A window is a nested object, so the tag is the interesting half: the bytes
  // the sender signs are the consumer's own canonical ones, and a window that
  // did not survive them would be a registration the consumer refuses.
  assert.equal(validateMessage(record).ok, true);
  assert.equal(verifyMac(record, MAC_KEY, 'inbox'), true);

  const off = await w.door.clearQuietHours();
  assert.equal(off.ok, true);
  assert.equal(off.published.ok, true);
  assert.equal('quiet_hours' in (await w.inbox())[1].prefs, false, 'the window is gone from the record, not set to zeroes');
});

test('quiet hours with no zone to name are refused rather than stored in the wrong hours', async () => {
  // No zone port at all: the device cannot say which zone it is in, and a window
  // stored against a UTC offset is a window in the wrong hours twice a year
  // (§3.8, Q5).
  const w = await world();
  const answer = await w.door.setQuietHours({ start: 1320, end: 420 });
  assert.equal(answer.ok, false);
  assert.equal(answer.code, 'no-zone');
  assert.deepEqual(await w.inbox(), []);
});

test('the screen renders the last outcome and the switches, and never a code', async () => {
  const w = await world();
  // Before anything has been asked: the never phrase, D160's defaults, and no
  // window -- what the phone would send if the owner never touched a switch.
  const fresh = await w.door.screen();
  assert.equal(fresh.state.ok, false);
  assert.deepEqual(fresh.controls.map((control) => control.on), [true, true, true, false, true]);
  assert.equal(fresh.quietHours.on, false);

  await w.door.register();
  const registered = await w.door.screen();
  assert.equal(registered.state.ok, true);

  // A refusal the table does not know is the waiting phrase -- the outcome the
  // screen renders is the registration's own state, which carries the code.
  const refused = await world({ token: new MemoryTokenSource({ token: null, fail: false }) });
  await refused.door.register();
  assert.equal(refused.door.state().code, TOKEN_CODES.NO_TOKEN);
  const line = (await refused.door.screen()).state.text;
  assert.equal(line.includes(TOKEN_CODES.NO_TOKEN), false, 'a code is a word for the app, never for the owner');
  assert.notEqual(line, PUSH_WAITING, 'and the phrase for a missing token is its own');
  assert.equal(line.includes('token'), true, 'it says what is missing, in words');
});

/**
 * A handful of event-loop turns, so a promise chain that is already underway
 * reaches its next await before the test does anything else. A macrotask drains
 * the microtasks queued before it, and the in-memory ports of a test are all
 * microtasks, so three rounds are more than a registration's own chain needs.
 */
function turns() {
  const turn = () => new Promise((resolve) => { setImmediate(resolve); });
  return turn().then(turn).then(turn);
}

/**
 * A token source whose FIRST call is held open until the test releases it: the
 * relay's network call is the slow half of a registration, and holding it is
 * what makes two flips overlap on the record. `started` resolves when the first
 * call is inside the source, so the test can start the second flip at exactly
 * the moment the first one is waiting.
 */
function slowFirstToken() {
  let calls = 0;
  let began;
  const started = new Promise((resolve) => { began = resolve; });
  let release;
  const held = new Promise((resolve) => { release = resolve; });
  return {
    started,
    release,
    token: async () => {
      calls += 1;
      if (calls === 1) {
        began();
        await held;
      }
      return { ok: true, token: TOKEN };
    },
  };
}

test('two flips that overlap on a slow token land the later record last, with both switches', async () => {
  // The hazard this test exists for (the design's §3.8 read with the consumer's
  // own order): a registration reads the switches, then awaits the token, then
  // writes with the client's `at`. The consumer applies inbox items in `at`
  // order and replaces `prefs` whole, so two overlapping registrations that read
  // the record out of order would land the OLDER settings with the LATER `at`
  // and the owner's newest flip would never reach the sender.
  let clock = 1_700_000_000_000;
  const token = slowFirstToken();
  const w = await world({ token, now: () => (clock += 1) });
  // A switch the first flip does not touch, so the second read has something to
  // be wrong about: without the queue, the second registration's read can be the
  // one that happened BEFORE the first flip was stored.
  const first = w.door.setControl('kill_switch', false);
  await token.started;
  const second = w.door.setControl('prompt', false);
  // Let the event loop run before the first call's token arrives: an
  // implementation with no queue lets the second registration reach its write in
  // these turns, which is what makes the FIRST call's stale record the one that
  // lands last -- the ordering this test is about. With the queue the second
  // registration cannot run at all until the first finishes, so this wait
  // changes nothing there.
  await turns();
  token.release();
  const [a, b] = await Promise.all([first, second]);
  // Both flips are stored and both are published -- this test is about which
  // record wins, not about one of them being dropped.
  for (const [answer, what] of [[a, 'kill_switch'], [b, 'prompt']]) {
    assert.equal(answer.ok, true, what);
    assert.equal(answer.published.ok, true, what);
  }

  // The consumer's own order: `at`, then the id (`internal/mailbox/inbox.go`).
  const ordered = (await w.inbox())
    .slice()
    .sort((x, y) => (x.at === y.at ? (String(x.id) < String(y.id) ? -1 : 1) : x.at - y.at));
  assert.equal(ordered.length, 2, 'each flip writes its own registration');
  const last = ordered.at(-1);
  // The one the sender obeys: the LAST write carries BOTH switches, so the
  // record the consumer keeps has the owner's two choices and not just the one
  // it happened to read last. Mutation: take the queue out of `registration.js`
  // and this write carries whichever record its read saw, one flip behind.
  assert.deepEqual(
    { kill_switch: last.prefs.kill_switch, prompt: last.prefs.prompt },
    { kill_switch: false, prompt: false },
    `the last registration carries ${JSON.stringify(last.prefs)}`,
  );
  assert.equal(last.at > ordered[0].at, true, 'and it is the later of the two, which is what the sender applies last');
  // ... and both records are registrations the consumer would take: the whole
  // path D113 is about, not a stub's word for it.
  for (const record of ordered) {
    assert.equal(record.kind, 'registration');
    assert.equal(validateMessage(record).ok, true);
    assert.equal(verifyMac(record, MAC_KEY, 'inbox'), true);
  }
});

test('two accounts over one settings store are two records, and two doors', async () => {
  const w = await world({ zone: () => ZONE });
  const other = createPush({
    client: w.client,
    settings: w.settings,
    accountId: 'phone',
    zone: () => ZONE,
    token: w.token,
    platform: 'android',
  });
  assert.notEqual(w.door.key, other.key);
  const flipped = await w.door.setControl('consumer_offline', false);
  assert.equal(flipped.ok, true);
  // The other account's record is untouched: it still reads as D160's defaults,
  // which is what the phone would send for it.
  const held = await other.prefs();
  assert.equal(held.ok, true);
  assert.equal(held.prefs.consumer_offline, true);
  assert.equal(held.stored, false, 'the other account never wrote one');
});
