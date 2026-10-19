// The app's mailbox client (mobile plan, Task P3.6; D111, D113, D113b, D118).
//
// The plan writes this task's test list as a list of things the client does
// **not** do, and this file is in that order:
//
//   * one message per utterance, with a fresh id, and **no other path** --
//     asserted over the whole write set of a scenario, not per call;
//   * **no unsigned write is ever attempted**: with no key the refusal happens
//     before the store is called, and every record that does reach the store
//     carries a tag over P6.3's canonical bytes that verifies under the key
//     from platform secure storage;
//   * reads are exactly its own outbox and the heartbeat, in that order, and
//     **the heartbeat is never written** (D113b -- it is the orchestrator's);
//   * an outbox message or a heartbeat whose tag does not verify is dropped:
//     not spoken, not acked, and never able to announce a live orchestrator;
//   * **it acks only after it has spoken**, on the message's own `acked` child
//     and nowhere else -- asserted as an order and not only as a fact;
//   * a re-delivery after a reconnect is the same record, and P1.1 answers it
//     as a duplicate, so the consumer does not act on it twice.
//
// It runs against the fakes this directory ships (`MemoryMailboxStore`,
// `MemorySpeaker`, P3.1's `MemorySecureStore`) and, for the duplicate case,
// against P1.1 itself -- with no network, no device and no Firebase project
// (Global Constraint 10). The only two real modules it leans on are the ones
// the client is built from: P6.3's schema (`macOf`, used to sign a fixture the
// way the consumer's other end would) and P1.1's state machine.
//
// Every secret-shaped string below is synthetic and is not recorded from any
// installation (`belong` least of all, Global Constraint 11).

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import { KIND, OUTCOME, REFUSAL, initialState, step } from '../../../shared/mailbox/index.js';
import { REFUSALS as SCHEMA_REFUSALS, validateHeartbeat, validateMessage } from '../../../shared/mailbox-schema/index.js';
import { REASON, STATE, evaluatePresence } from '../../../shared/presence/index.js';
import { MemorySecureStore, SecretStore, secret } from '../accounts/index.js';
import { CLIENT_CODES, MAILBOX_TTL_MS, PRESENCE_REASON, createMailboxClient, toPresenceHeartbeat } from './client.js';
import { MemoryConnection } from './connection.js';
import { GATE_CODES, OFFLINE_SPEECH } from './gate.js';
import { macOf } from './mac.js';
import { MemoryMailboxStore, STORE_CODES } from './store.js';
import { MemorySpeaker } from './speaker.js';

/** A mailbox id shaped like an Auth uid, and never a real one. */
const UID = 'AbCdEf0123456789ghIJkl';

/** D114's tuple, with D111's item for the pairing key. */
const ACCOUNT = Object.freeze({
  id: 'laptop',
  firebaseProjectId: 'fugaro-dev',
  authUid: UID,
  fugaroProject: 'fugaro',
});

const T0 = 1_700_000_000_000;
const DAY_MS = 24 * 60 * 60 * 1_000;
const INTERVAL_MS = 60_000;

/** D171's per-pairing key, synthetic: 32 bytes of hex is what the store holds. */
const MAC_KEY = 'synthetic-mac-key-0123456789abcdef';
/** D118's secret list: what must not leave the device. */
const SECRETS = ['synthetic-token-0123456789'];
/** Another pairing's key, for the "the tag is not this pairing's" cases. */
const OTHER_KEY = 'synthetic-mac-key-fedcba9876543210';

const INBOX = `mailbox/${UID}/inbox`;
const OUTBOX = `mailbox/${UID}/outbox`;
const HEARTBEAT = `mailbox/${UID}/heartbeat`;

/** A message as this pairing signs it, for the phone to read out of its outbox. */
function signed(record, direction, key = MAC_KEY) {
  return { ...record, mac: macOf(record, key, direction) };
}

function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * The store the client sees, with its own log of what the CLIENT asked for.
 *
 * The fixtures write through `db` instead, so `writes()` answers "what did the
 * client write" and not "what does the database hold" -- which is the question
 * D113 is about. The call order is recorded in `events`, so a test can assert
 * that the speak happens before the ack rather than next to it.
 */
class LoggedStore {
  #db;
  #events;
  #writes = [];
  #reads = [];

  constructor(db, events) {
    this.#db = db;
    this.#events = events;
  }

  /** The database behind the port, for plantings and for raw inspection. */
  get db() {
    return this.#db;
  }

  async write(path, value) {
    // The attempt is recorded whether or not it lands: the client's report is
    // about what it tried, and "no unsigned write was attempted" is a claim
    // about attempts.
    this.#writes.push(Object.freeze({ path, value }));
    this.#events.push(`write:${path}`);
    return this.#db.write(path, value);
  }

  async read(path) {
    this.#reads.push(Object.freeze({ path }));
    this.#events.push(`read:${path}`);
    return this.#db.read(path);
  }

  writes() {
    return Object.freeze([...this.#writes]);
  }

  reads() {
    return Object.freeze([...this.#reads]);
  }
}

/** A speaker that records the order it spoke in, beside the store's writes. */
class LoggedSpeaker extends MemorySpeaker {
  #events;

  constructor(events, options = {}) {
    super(options);
    this.#events = events;
  }

  async speak(text) {
    const said = await super.speak(text);
    if (said.ok) this.#events.push(`speak:${text}`);
    return said;
  }
}

/**
 * A paired phone, its database, its speaker and the ports to inspect.
 *
 * `now` is a variable rather than a clock: nothing here reads the time, and a
 * test that wants "an hour later" says so. The id source mints `m-1`, `m-2`, …
 * unless a test supplies its own (the id-conflict case does).
 */
async function harness(options = {}) {
  const events = [];
  const db = options.db ?? new MemoryMailboxStore();
  const store = new LoggedStore(db, events);
  const speaker = options.speaker ?? new LoggedSpeaker(events);
  const keys = options.keys ?? new SecretStore(new MemorySecureStore());
  // P6.10's port. It defaults to ONLINE and to a consumer nothing has looked at
  // yet, which is the state every test that is not about the gate starts in: a
  // suite about signing and acking must not silently be a suite about being
  // offline, and "nothing has been observed" is not evidence that the consumer
  // is down (`gate.js` says why).
  const connection = options.connection ?? new MemoryConnection();
  // The key is stored under the INSTALLATION's id, which is not the mailbox's
  // (D114 vs the rules' `$mb == auth.uid`): the two tests that move the account
  // tuple are about exactly that.
  if (options.pair !== false) await pair(keys, options.key ?? MAC_KEY, (options.account ?? ACCOUNT).id);
  let nowMs = options.startMs ?? T0;
  let minted = 0;
  const client = createMailboxClient({
    account: options.account ?? ACCOUNT,
    keys,
    store,
    connection,
    speaker,
    now: () => nowMs,
    newId: options.newId ?? (() => `m-${++minted}`),
    secrets: options.secrets ?? SECRETS,
    heartbeatIntervalMs: options.heartbeatIntervalMs ?? INTERVAL_MS,
    ...(options.mailboxTtlMs === undefined ? {} : { mailboxTtlMs: options.mailboxTtlMs }),
  });
  return {
    client,
    store,
    speaker,
    keys,
    connection,
    db,
    events,
    paths: client.paths,
    setNow: (value) => {
      nowMs = value;
    },
    advance: (ms) => {
      nowMs += ms;
    },
    now: () => nowMs,
    valueAt: (path) => db.valueAt(path),
  };
}

/**
 * P3.1's `SecretStore`, with a log of how many times the material was asked
 * for. "The key was not touched" is a claim about the ASK, not about what the
 * store answered, so the count is the thing a test asserts.
 */
function countingKeys(inner) {
  const asked = [];
  return {
    asked: () => Object.freeze([...asked]),
    put: (accountId, item, value) => inner.put(accountId, item, value),
    use: (accountId, item, fn) => {
      asked.push(item);
      return inner.use(accountId, item, fn);
    },
  };
}

/** Put the pairing key in the store, the way `fugaro pair` would (D111). */
async function pair(keys, key = MAC_KEY, accountId = ACCOUNT.id) {
  const made = secret('macKey', key);
  assert.equal(made.ok, true, 'the fixture key is a valid secret');
  const written = await keys.put(accountId, 'macKey', made.secret);
  assert.equal(written.ok, true, 'the fixture key is stored');
}

/** Plant a signed outbox message, as the consumer's other end would write it. */
async function plantOutbox(h, id, fields = {}, options = {}) {
  const { direction = 'outbox', key = MAC_KEY, tamper } = options;
  const base = {
    id,
    sender: UID,
    text: 'run 42 finished',
    at: h.now() - 1_000,
    ...fields,
  };
  // The tag is made over the record as it will be written, and `tamper` runs
  // AFTER the signature -- which is what a forger on the wire does. One write,
  // so the database's own `!data.exists()` never has to be worked around.
  const signedRecord = signed(base, direction, key);
  const record = tamper === undefined ? signedRecord : tamper(signedRecord);
  await h.db.write(`${OUTBOX}/${id}`, record);
  return record;
}

/** Plant a signed heartbeat at the mailbox's heartbeat node. */
async function plantHeartbeat(h, fields = {}, { direction = 'heartbeat', key = MAC_KEY, raw } = {}) {
  const base = { at: h.now() - 1_000, boot: 'boot-0001', ...fields };
  const record = raw === undefined ? signed(base, direction, key) : raw;
  await h.db.write(HEARTBEAT, record);
  return record;
}

/**
 * Every write the client asked for is one of D113's two paths: a message node
 * under its own inbox, or an `acked` child of a message in its own outbox.
 * There is no third path, and the heartbeat is never among them (D113b).
 */
function assertD113WriteSet(store, paths) {
  for (const { path, value } of store.writes()) {
    if (path.startsWith(`${paths.inbox}/`)) {
      assert.match(value.mac, /^[0-9a-f]{64}$/, `an inbox write with no tag: ${path}`);
      assert.equal(validateMessage(value).ok, true, `an inbox write the schema refuses: ${path}`);
      continue;
    }
    if (path.startsWith(`${paths.outbox}/`) && path.endsWith('/acked')) {
      assert.equal(value, true, `the ack must be exactly true: ${path}`);
      continue;
    }
    assert.fail(`a write outside D113's two paths: ${path}`);
  }
  assert.equal(
    store.writes().some((entry) => entry.path === paths.heartbeat),
    false,
    'the heartbeat is the orchestrator\'s write, never the phone\'s',
  );
}

// ---------------------------------------------------------------------------
// The wiring
// ---------------------------------------------------------------------------

test('every port and every value source is required, and nothing is guessed', () => {
  const base = {
    account: ACCOUNT,
    keys: new SecretStore(new MemorySecureStore()),
    store: new MemoryMailboxStore(),
    connection: new MemoryConnection(),
    speaker: new MemorySpeaker(),
    now: () => T0,
    newId: () => 'm-1',
    secrets: SECRETS,
    heartbeatIntervalMs: INTERVAL_MS,
  };
  assert.equal(createMailboxClient(base).ok, true);
  // A missing port is a wiring bug, not a refusal a caller can ignore: a
  // client built without a key store would fail at the first utterance, with
  // the owner waiting.
  for (const field of ['account', 'keys', 'store', 'connection', 'speaker', 'now', 'newId', 'secrets', 'heartbeatIntervalMs']) {
    const { [field]: _gone, ...rest } = base;
    assert.throws(() => createMailboxClient(rest), TypeError, `without ${field}`);
  }
  for (const field of ['keys', 'store', 'connection', 'speaker']) {
    assert.throws(() => createMailboxClient({ ...base, [field]: {} }), TypeError, field);
  }
  assert.throws(() => createMailboxClient(null), TypeError);
  assert.throws(() => createMailboxClient([]), TypeError);
  // The secret list is P1.3's input: a list that is not a list of strings is
  // refused here, where the app is being wired.
  for (const secrets of [undefined, null, 'a-secret', 42, {}, ['ok', 42]]) {
    assert.throws(() => createMailboxClient({ ...base, secrets }), TypeError, String(secrets));
  }
  // D117's interval, with P1.4's floor and ceiling: outside the band is a
  // wiring failure rather than something that surfaces from a poll.
  for (const heartbeatIntervalMs of [29_999, 0, -1, 1.5, 24 * 60 * 60 * 1_000 + 1, 'x', null]) {
    assert.throws(() => createMailboxClient({ ...base, heartbeatIntervalMs }), TypeError, String(heartbeatIntervalMs));
  }
  for (const mailboxTtlMs of [0, -1, 1.5, 'x', null]) {
    assert.throws(() => createMailboxClient({ ...base, mailboxTtlMs }), TypeError, String(mailboxTtlMs));
  }
});

test('an account tuple that is not usable is a wiring failure carrying P3.1\'s code', () => {
  // The tuple is owner-typed, so its refusal is P3.1's -- and the message must
  // not echo what was in it (a tuple is printed nowhere).
  const broken = { ...ACCOUNT, id: 'lap top' };
  assert.throws(
    () => createMailboxClient({
      account: broken,
      keys: new SecretStore(new MemorySecureStore()),
      store: new MemoryMailboxStore(),
      connection: new MemoryConnection(),
      speaker: new MemorySpeaker(),
      now: () => T0,
      newId: () => 'm-1',
      secrets: SECRETS,
      heartbeatIntervalMs: INTERVAL_MS,
    }),
    (error) => {
      assert.equal(error instanceof TypeError, true);
      assert.equal(typeof error.code, 'string');
      assert.equal(error.message.includes('lap top'), false);
      return true;
    },
  );
});

test('the client is frozen, and so are the paths it writes to', async () => {
  const h = await harness();
  assert.equal(Object.isFrozen(h.client), true);
  assert.equal(Object.isFrozen(h.paths), true);
  assert.equal(h.paths.mailbox, `mailbox/${UID}`);
  assert.equal(h.paths.inbox, INBOX);
  assert.equal(h.paths.outbox, OUTBOX);
  assert.equal(h.paths.heartbeat, HEARTBEAT);
  assert.equal(h.paths.inboxMessage('m-1'), `${INBOX}/m-1`);
  assert.equal(h.paths.outboxMessage('r-1'), `${OUTBOX}/r-1`);
  assert.equal(h.paths.acked('r-1'), `${OUTBOX}/r-1/acked`);
  assert.throws(() => {
    'use strict';
    h.client.send = () => {};
  }, TypeError);
});

test('the client object is exactly its seven names, and nothing else is on it', async () => {
  // `index.test.js` pins the DOOR's export set; the object the builder returns
  // is the other half of the same rule, and it is the one a screen holds. A
  // `raw(path, value)` added here "for one screen" would be a second write path
  // with its own key handling -- the thing D113 and this whole task are about.
  // `unsent` is P6.10's one addition, and it is a read: the utterances the gate
  // refused, which nothing here will ever send.
  const h = await harness();
  assert.deepEqual(Object.keys(h.client).sort(), ['flush', 'ok', 'paths', 'pending', 'poll', 'send', 'unsent']);
  for (const name of ['raw', 'write', 'read', 'store', 'keys', 'key', 'mac', 'database', 'ref']) {
    assert.equal(name in h.client, false, name);
  }
});

test('the mailbox is the account\'s uid, and the key is the account\'s id', async () => {
  // D114's tuple names the installation (the keychain item); the rules' `$mb ==
  // auth.uid` names the mailbox. A client that conflated them would write to
  // another mailbox's path and sign with the wrong item's key.
  const h = await harness({ account: { ...ACCOUNT, id: 'desk', authUid: 'ZzYyXx0987654321abCDef' } });
  assert.equal(h.paths.inbox, 'mailbox/ZzYyXx0987654321abCDef/inbox');
  const sent = await h.client.send('run 42 finished');
  assert.deepEqual(sent, { ok: true, id: 'm-1', at: T0 });
  assert.deepEqual(h.store.writes().map((entry) => entry.path), ['mailbox/ZzYyXx0987654321abCDef/inbox/m-1']);
  assert.equal(h.store.writes()[0].value.sender, 'ZzYyXx0987654321abCDef');
});

// ---------------------------------------------------------------------------
// One message per utterance, signed, on one path
// ---------------------------------------------------------------------------

test('one utterance is one message, with a fresh id each time', async () => {
  const h = await harness();
  assert.deepEqual(await h.client.send('run 42 finished'), { ok: true, id: 'm-1', at: T0 });
  h.advance(1_000);
  assert.deepEqual(await h.client.send('merge it'), { ok: true, id: 'm-2', at: T0 + 1_000 });
  assert.deepEqual(h.store.writes().map((entry) => entry.path), [`${INBOX}/m-1`, `${INBOX}/m-2`]);
  assertD113WriteSet(h.store, h.paths);
});

test('the message it writes is exactly the five fields, tagged over the schema\'s bytes', async () => {
  const h = await harness();
  h.advance(2_500);
  await h.client.send('run 42 finished');
  const written = h.valueAt(`${INBOX}/m-1`);
  assert.deepEqual(Object.keys(written), ['id', 'sender', 'text', 'at', 'mac']);
  assert.equal(written.id, 'm-1');
  assert.equal(written.sender, UID);
  assert.equal(written.text, 'run 42 finished');
  assert.equal(written.at, T0 + 2_500);
  // The tag is P6.3's, over the record without it, for the INBOX direction --
  // and it verifies under the key the platform store holds, which the client
  // itself never kept.
  assert.equal(written.mac, macOf({ id: 'm-1', sender: UID, text: 'run 42 finished', at: T0 + 2_500 }, MAC_KEY, 'inbox'));
  assert.equal(validateMessage(written).ok, true);
});

test('no unsigned write is ever attempted: without a key the store is not called', async () => {
  // The plan's sentence, as a test. Three ways to have no usable key, and in
  // all three the write set stays empty -- an unsigned message is never
  // *attempted*, not merely never landed.
  const unpaired = await harness({ pair: false });
  assert.deepEqual(await unpaired.client.send('run 42 finished'), refuse(CLIENT_CODES.NO_KEY, 'keys'));
  assert.deepEqual(unpaired.store.writes(), []);

  // The item is there and cannot be read (a value this module could not have
  // written): a different fact, and a different code.
  const port = new MemorySecureStore();
  await port.setItem(`fugaro.account.${ACCOUNT.id}.macKey`, 42);
  const unreadable = await harness({ keys: new SecretStore(port), pair: false });
  assert.deepEqual(await unreadable.client.send('run 42 finished'), refuse(CLIENT_CODES.KEY_UNREADABLE, 'keys'));
  assert.deepEqual(unreadable.store.writes(), []);

  // ... and the key is not in the refusal.
  assert.equal(JSON.stringify(await unreadable.client.send('x')).includes(MAC_KEY), false);
});

test('a pairing key short enough to turn up inside its own tag still speaks', async () => {
  // The leak fence in P3.1's `SecretStore.use` walks what the callback returned,
  // and this client's callback returns the tag it made with the key. A short key
  // is inside a 64-hex tag by chance -- `ab` is, in this very record -- and a
  // fence that read that coincidence as "the material came back out" would
  // refuse EVERY send as `key-unreadable`, which is the store's word for a key
  // that cannot be used at all. The fence's own test is
  // `accounts/secure-store.test.js`'s; this is the same fact at the boundary
  // the owner meets, where the cost of getting it wrong is a phone that cannot
  // speak.
  const SHORT_KEY = 'ab';
  const h = await harness({ key: SHORT_KEY });
  const tag = macOf({ id: 'm-1', sender: UID, text: 'run 42 finished', at: T0 }, SHORT_KEY, 'inbox');
  assert.equal(tag.includes(SHORT_KEY), true, 'the coincidence this test is about');
  assert.deepEqual(await h.client.send('run 42 finished'), { ok: true, id: 'm-1', at: T0 });
  assert.equal(h.valueAt(`${INBOX}/m-1`).mac, tag, 'and the tag is the tag that key makes');
  assertD113WriteSet(h.store, h.paths);
  // A poll reads the consumer's reply with the same short key, so the fence
  // cannot refuse one direction and not the other.
  await plantOutbox(h, 'r-1', {}, { key: SHORT_KEY });
  const report = await h.client.poll();
  assert.deepEqual(report.spoken, ['r-1']);
  assert.equal(report.presence.state, STATE.OFFLINE, 'an empty heartbeat node, and nothing more is claimed');
});

test('the tag is over the REDACTED text, so a secret is in neither the bytes nor the tag', async () => {
  // D118's ordering, which is why redaction is a step in `send` and not
  // something the database adapter does: what is signed is what is written.
  const h = await harness();
  const said = `deploy with ${SECRETS[0]} please`;
  await h.client.send(said);
  const written = h.valueAt(`${INBOX}/m-1`);
  assert.equal(written.text, 'deploy with [REDACTED] please');
  assert.equal(JSON.stringify(h.store.writes()).includes(SECRETS[0]), false);
  assert.equal(
    written.mac,
    macOf({ id: 'm-1', sender: UID, text: 'deploy with [REDACTED] please', at: T0 }, MAC_KEY, 'inbox'),
  );
  // The unredacted utterance is not recoverable from the tag either: it is a
  // keyed digest of the redacted bytes.
  assert.equal(written.mac.includes(SECRETS[0]), false);
});

test('a text that is not text, or too long, is refused before anything is signed', async () => {
  const h = await harness();
  for (const text of [undefined, null, 42, {}, ['a'], '']) {
    assert.deepEqual(await h.client.send(text), refuse(CLIENT_CODES.BAD_TEXT, 'text'), String(text));
  }
  assert.deepEqual(await h.client.send('a'.repeat(4097)), refuse(CLIENT_CODES.TOO_LONG, 'text'));
  // The bound itself is the schema's, and the schema's own limit passes.
  assert.equal((await h.client.send('a'.repeat(4096))).ok, true);
  assert.equal(h.store.writes().length, 1, 'only the utterance that fits was attempted');
});

test('a text nobody can sign is refused as unsignable, and never as key-unreadable', async () => {
  // `canonicalBytes` throws for the characters the two languages spell
  // differently -- U+2028 and U+2029, which Go's encoder escapes and
  // JavaScript's does not, and an unpaired surrogate -- so the tag cannot be
  // computed for them at all. That throw happens INSIDE the key's callback,
  // where it would otherwise be indistinguishable from a Keychain that would not
  // open: the owner would be sent to re-pair over a character in their own
  // sentence. It is asked before the key is touched instead.
  const h = await harness({ keys: countingKeys(new SecretStore(new MemorySecureStore())), newId: () => 'm-1' });
  for (const [name, text] of [
    ['U+2028 LINE SEPARATOR', 'run 42 finished'],
    ['U+2029 PARAGRAPH SEPARATOR', 'run 42 finished'],
    ['an unpaired high surrogate', 'run 42\ud800finished'],
    ['an unpaired low surrogate', 'run 42\udc00finished'],
  ]) {
    assert.deepEqual(await h.client.send(text), refuse(CLIENT_CODES.UNSIGNABLE, 'text'), name);
  }
  assert.deepEqual(h.keys.asked(), [], 'the key was not even asked for');
  assert.deepEqual(h.store.writes(), [], 'and nothing was written');
  assert.deepEqual(h.client.pending(), [], 'nor is an unsendable utterance something to show as pending');
  // Nothing was recorded as used either: the id source's first id is still
  // free, so the same sentence without the character is a message.
  const sent = await h.client.send('run 42 finished');
  assert.deepEqual(sent, { ok: true, id: 'm-1', at: T0 });
  assert.equal(h.keys.asked().length, 1, 'the key is asked for once, by the send that could be signed');
  assert.deepEqual(h.store.writes().map((entry) => entry.path), [`${INBOX}/m-1`]);
  assertD113WriteSet(h.store, h.paths);
});

test('a control character is refused with the schema\'s reason, and nothing is written', async () => {
  // The other half of the same question, and a different answer: U+0007 and its
  // neighbours ARE signable (`JSON.stringify` escapes them the same way in both
  // languages), so the tag is made -- and it is the schema that refuses the
  // record, with its own code, before the write. Deleting that validation would
  // leave a message carrying a control character in the inbox, so this test is
  // what holds the validation in place.
  const h = await harness();
  for (const [name, text] of [
    ['U+0000 NUL', 'run\u000042'],
    ['U+0007 BELL', 'run 42\u0007'],
    ['U+000B LINE TABULATION', 'run\u000b42'],
    ['U+007F DELETE', 'run 42\u007f'],
  ]) {
    assert.deepEqual(await h.client.send(text), refuse(SCHEMA_REFUSALS.BAD_CHARACTER, 'text'), name);
  }
  assert.deepEqual(h.store.writes(), [], 'a record the schema refuses is never attempted');
  assert.deepEqual(h.client.pending(), []);
  // The edge of that rule is the SCHEMA's and not this module's: its text rule
  // is the C0 controls and DEL, so a line break, a tab and a C1 control are all
  // sent -- D118 redacts and signs a sentence, it does not flatten it, and a
  // second character rule here would be a rule the consumer does not share.
  for (const text of ['run 42\nfinished', 'run 42\tfinished', 'run 42\r\nfinished', 'run 42\u009cfinished']) {
    assert.equal((await h.client.send(text)).ok, true, JSON.stringify(text));
  }
  assertD113WriteSet(h.store, h.paths);
});

test('a clock that is not a time, and an id that is not a database key, are refused', async () => {
  const h = await harness();
  for (const value of [0, -1, 1.5, Number.NaN, 'now', undefined]) {
    h.setNow(value);
    assert.deepEqual(await h.client.send('run 42 finished'), refuse(CLIENT_CODES.BAD_CLOCK, 'now'), String(value));
  }
  assert.deepEqual(h.store.writes(), [], 'a message with no time is not written');
  h.setNow(T0);
  for (const bad of ['', 'a/b', 'a.b', 'a%b', 42, null, undefined, 'x'.repeat(101)]) {
    const client = createMailboxClient({
      account: ACCOUNT,
      keys: h.keys,
      store: h.store,
      connection: h.connection,
      speaker: h.speaker,
      now: () => T0,
      newId: () => bad,
      secrets: SECRETS,
      heartbeatIntervalMs: INTERVAL_MS,
    });
    assert.deepEqual(await client.send('run 42 finished'), refuse(CLIENT_CODES.BAD_ID, 'newId'), String(bad));
  }
  assert.deepEqual(h.store.writes(), [], 'an id the rules would refuse is not attempted either');
});

test('an id that has already been used is not used twice', async () => {
  // P2.1's template denies a second write to a node that exists, so a reused id
  // means the consumer keeps the FIRST message and this utterance is lost
  // silently. The id source's bug is caught before anything is signed.
  const h = await harness({ newId: () => 'm-1' });
  assert.equal((await h.client.send('run 42 finished')).ok, true);
  h.advance(1_000);
  assert.deepEqual(await h.client.send('merge it'), refuse(CLIENT_CODES.ID_CONFLICT, 'newId'));
  assert.deepEqual(h.store.writes().map((entry) => entry.path), [`${INBOX}/m-1`]);
  assert.equal(h.valueAt(`${INBOX}/m-1`).text, 'run 42 finished', 'the first utterance stands');
  assert.equal(h.valueAt(`${INBOX}/m-1`).at, T0, 'and its own time, not the second one\'s');
  // Nothing is pending: the refused utterance was never a message.
  assert.deepEqual(h.client.pending(), [
    { id: 'm-1', at: T0, text: 'run 42 finished', landed: true },
  ]);
});

// ---------------------------------------------------------------------------
// The reconnect: re-send, verbatim, until the outbox answers
// ---------------------------------------------------------------------------

test('a write that does not land is kept, reported, and re-sent byte for byte', async () => {
  const h = await harness();
  h.store.db.setFailures({ failWrites: true });
  const refused = await h.client.send('run 42 finished');
  assert.equal(refused.ok, false);
  assert.equal(refused.code, STORE_CODES.WRITE_FAILED, 'the store\'s own code, not a new one');
  assert.deepEqual(h.client.pending(), [
    { id: 'm-1', at: T0, text: 'run 42 finished', landed: false },
  ]);
  // The phone comes back. The re-send is the SAME record: same id, same `at`,
  // same tag (P1.1's caller duty -- never retry an id with a new `at`).
  h.store.db.setFailures({ failWrites: false });
  h.advance(30_000);
  const flushed = await h.client.flush();
  assert.deepEqual(flushed.written, ['m-1']);
  assert.deepEqual(flushed.failed, []);
  assert.deepEqual(flushed.outstanding, ['m-1']);
  const attempts = h.store.writes();
  assert.equal(attempts.length, 2);
  assert.deepEqual(attempts[1].value, attempts[0].value);
  assert.equal(h.valueAt(`${INBOX}/m-1`).at, T0, 'stamped when it was said, not when it landed');
  assert.deepEqual(h.client.pending(), [
    { id: 'm-1', at: T0, text: 'run 42 finished', landed: true },
  ]);
  assertD113WriteSet(h.store, h.paths);
});

test('a message that is already there is a landed write, not a failure', async () => {
  // The template's `!data.exists()` denies a re-write, and the phone cannot
  // tell that denial from "you are not allowed". Reading it as landed is what
  // makes a retry after a lost acknowledgement safe.
  const h = await harness();
  await h.client.send('run 42 finished');
  const first = h.valueAt(`${INBOX}/m-1`);
  const again = await h.client.flush();
  assert.deepEqual(again.written, ['m-1'], 'the re-write was denied, and that is "it is there"');
  assert.deepEqual(again.failed, []);
  assert.deepEqual(h.valueAt(`${INBOX}/m-1`), first, 'and the first message stands');
  // The same holds for a store that quietly accepts the re-write: the client
  // does not depend on the denial. What is asserted is that the re-write really
  // LANDED there -- the node is written a second time, and what stands
  // afterwards is the record the client re-sent and not whatever was at the
  // node. A planted value is what makes the two distinguishable: with the
  // denial in place there is nothing to overwrite, so only this half can show
  // the second write was accepted.
  const permissive = await harness({ db: new MemoryMailboxStore({ existsRefuses: false }) });
  await permissive.client.send('run 42 finished');
  const stamped = permissive.valueAt(`${INBOX}/m-1`);
  await permissive.db.write(`${INBOX}/m-1`, { ...stamped, text: 'something else' });
  assert.deepEqual((await permissive.client.flush()).written, ['m-1']);
  assert.deepEqual(permissive.valueAt(`${INBOX}/m-1`), stamped, 'the re-send is what stands');
  assert.equal(permissive.valueAt(`${INBOX}/m-1`).text, 'run 42 finished', 'the planted text is gone');
});

test('flush sends nothing when the outbox has answered everything', async () => {
  const h = await harness();
  assert.deepEqual(await h.client.flush(), { ok: true, written: [], failed: [], expired: [], outstanding: [] });
  assert.deepEqual(h.store.writes(), []);
  await h.client.send('run 42 finished');
  h.store.db.setFailures({ failWrites: true });
  const flushed = await h.client.flush();
  assert.deepEqual(flushed.failed, [{ id: 'm-1', code: STORE_CODES.WRITE_FAILED }]);
  assert.deepEqual(flushed.written, []);
  assert.deepEqual(flushed.outstanding, ['m-1'], 'still unanswered, so still re-sent');
});

test('a message still pending at D118\'s TTL is dropped and reported, not retried for ever', async () => {
  const h = await harness();
  h.store.db.setFailures({ failWrites: true });
  await h.client.send('run 42 finished');
  assert.equal(MAILBOX_TTL_MS, 72 * 60 * 60 * 1_000, 'D118\'s number, in the plan (O5)');
  // At the boundary the phone still tries, because the comparison is `>` and
  // not `>=`: exactly 72 h is still inside the mailbox TTL.
  h.advance(MAILBOX_TTL_MS);
  assert.deepEqual((await h.client.flush()).expired, []);
  // One millisecond past it, the phone stops -- and says so.
  h.advance(1);
  const flushed = await h.client.flush();
  assert.deepEqual(flushed.expired, [{ id: 'm-1', code: 'expired' }]);
  assert.deepEqual(flushed.outstanding, []);
  assert.deepEqual(h.client.pending(), []);
  // A later flush does not resurrect it, and nothing was written past the
  // boundary: the two attempts are the send and the one at exactly 72h.
  assert.deepEqual((await h.client.flush()).expired, []);
  assert.deepEqual(h.store.writes().map((entry) => entry.path), [`${INBOX}/m-1`, `${INBOX}/m-1`]);
  assert.deepEqual(h.store.writes()[1].value, h.store.writes()[0].value, 'the same record, at the boundary');
});

test('the phone retries inside D118\'s 72 h, and the consumer accepts a command for 6', async () => {
  // **The two windows are not the same window, and the difference is in the
  // code rather than the prose.** `MAILBOX_TTL_MS` is D118's mailbox TTL -- the
  // owner's O5 number, 72 h -- and it is the RETENTION window the phone's retry
  // lives inside. What the consumer ACCEPTS is P1.1's per-kind window (D173): a
  // prompt inside `promptTtlMs`, and a command inside `windowMs` -- the 6-hour
  // replay window, P1.1's default. (The two are no relation: the prompt TTL is
  // the CONSUMER's setting and this module has no `promptTtlMs` option at all --
  // the client writes records with no `kind`. The `promptTtlMs` handed to P1.1
  // below is this test's, there only to show the two numbers are not one.) The
  // records this client writes carry no `kind`, so the consumer reads them as
  // commands, and a retry between the two windows is still made by the phone and
  // refused at the other end as a replay.
  const h = await harness();
  h.store.db.setFailures({ failWrites: true });
  await h.client.send('run 42 finished');
  const said = h.store.writes()[0].value;
  h.advance(10 * 60 * 60 * 1_000);
  const flushed = await h.client.flush();
  assert.deepEqual(flushed.expired, [], 'inside the mailbox TTL, so still tried');
  assert.deepEqual(flushed.outstanding, ['m-1'], 'and still unanswered, so still the phone\'s to re-send');
  assert.deepEqual(flushed.failed, [{ id: 'm-1', code: STORE_CODES.WRITE_FAILED }], 'the write is still attempted');
  // ... and the consumer, handed it at the same `now`, refuses it -- as a
  // replay, ten hours after it was said and long before D118's TTL.
  const opened = initialState({ now: h.now(), promptTtlMs: MAILBOX_TTL_MS });
  assert.equal(opened.ok, true);
  assert.equal(opened.state.windowMs, 6 * 60 * 60 * 1_000, 'P1.1\'s command window, its own default');
  const late = step(opened.state, { type: 'deliver', now: h.now(), message: said, kind: KIND.COMMAND });
  assert.equal(late.ok, false);
  assert.equal(late.code, REFUSAL.REPLAY);
  // The same record, an hour old, is accepted: the refusal above is about the
  // age and not about the message.
  const early = initialState({ now: T0 + 60 * 60 * 1_000, promptTtlMs: MAILBOX_TTL_MS });
  const soon = step(early.state, { type: 'deliver', now: T0 + 60 * 60 * 1_000, message: said, kind: KIND.COMMAND });
  assert.equal(soon.outcome, OUTCOME.ACCEPTED);
});

test('a clock that is not a time is refused by flush and by poll, and nothing is done first', async () => {
  // `send` is not the only method that reads the clock: `flush` decides the
  // TTL from it, and `poll` flushes and judges presence with it. A bad clock is
  // a refusal from both -- and neither may half-do its work first, which is
  // what `reads()` and the write log below are about.
  for (const value of [0, -1, 1.5, Number.NaN, 'now', undefined]) {
    const h = await harness();
    h.store.db.setFailures({ failWrites: true });
    await h.client.send('run 42 finished');
    h.setNow(value);
    assert.deepEqual(await h.client.flush(), refuse(CLIENT_CODES.BAD_CLOCK, 'now'), `flush ${String(value)}`);
    assert.equal(h.store.writes().length, 1, `flush re-sent nothing: ${String(value)}`);
    assert.deepEqual(await h.client.poll(), refuse(CLIENT_CODES.BAD_CLOCK, 'now'), `poll ${String(value)}`);
    assert.equal(h.store.writes().length, 1, `poll re-sent nothing: ${String(value)}`);
    assert.deepEqual(h.store.reads(), [], `poll read nothing: ${String(value)}`);
    assert.deepEqual(h.speaker.spoken(), []);
  }
});

test('poll without a usable key is refused before it speaks or acks', async () => {
  // The poll reads the key once, after its two reads and before any speak, so
  // the two key refusals `send` already has are the two it has: an absent key
  // is `no-key` (this phone is not paired) and an unreadable one is
  // `key-unreadable` (the item is there and cannot be used). Neither is a
  // reason to speak: what cannot be verified is not acted on.
  const unpaired = await harness({ pair: false });
  await plantOutbox(unpaired, 'r-1');
  assert.deepEqual(await unpaired.client.poll(), refuse(CLIENT_CODES.NO_KEY, 'keys'));
  assert.deepEqual(unpaired.speaker.spoken(), []);
  assert.deepEqual(unpaired.store.writes(), []);

  const port = new MemorySecureStore();
  await port.setItem(`fugaro.account.${ACCOUNT.id}.macKey`, 42);
  const unreadable = await harness({ keys: new SecretStore(port), pair: false });
  await plantOutbox(unreadable, 'r-1');
  assert.deepEqual(await unreadable.client.poll(), refuse(CLIENT_CODES.KEY_UNREADABLE, 'keys'));
  assert.deepEqual(unreadable.speaker.spoken(), []);
  assert.deepEqual(unreadable.store.writes(), []);
});

// ---------------------------------------------------------------------------
// The reads, and the replies
// ---------------------------------------------------------------------------

test('poll reads its own outbox and the heartbeat, in that order, and nothing else', async () => {
  const h = await harness();
  await h.client.poll();
  assert.deepEqual(h.store.reads(), [{ path: OUTBOX }, { path: HEARTBEAT }]);
  // The inbox is NOT readable by the phone (the (b1) grant): not once, in any
  // test in this file, does the client ask for it.
  assert.equal(h.store.reads().some((entry) => entry.path.startsWith(INBOX)), false);
});

test('a verified reply is spoken once and then acked on its own child, and nowhere else', async () => {
  const h = await harness();
  await plantOutbox(h, 'r-1');
  const report = await h.client.poll();
  assert.deepEqual(h.speaker.spoken(), ['run 42 finished']);
  assert.deepEqual(report.spoken, ['r-1']);
  assert.deepEqual(report.acked, ['r-1']);
  assert.deepEqual(report.dropped, []);
  assert.deepEqual(report.unspoken, []);
  assert.deepEqual(report.ackFailed, []);
  // The ack is the plan's second, explicitly-allowed write: the message's own
  // `acked` child, exactly `true`, and nowhere else.
  assert.deepEqual(h.store.writes().map((entry) => entry.path), [`${OUTBOX}/r-1/acked`]);
  assert.equal(h.valueAt(`${OUTBOX}/r-1/acked`), true);
  assertD113WriteSet(h.store, h.paths);
  // The ORDER, which is the rule: the reads, then the speak, then the ack.
  assert.deepEqual(h.events, [
    `read:${OUTBOX}`,
    `read:${HEARTBEAT}`,
    'speak:run 42 finished',
    `write:${OUTBOX}/r-1/acked`,
  ]);
});

test('it acks nothing it has not spoken, and a failed speak leaves the message unacked', async () => {
  const h = await harness();
  await plantOutbox(h, 'r-1');
  h.speaker.setFailing(true);
  const report = await h.client.poll();
  assert.deepEqual(report.unspoken, [{ id: 'r-1', code: 'speak-failed' }]);
  assert.deepEqual(report.spoken, []);
  assert.deepEqual(report.acked, []);
  assert.deepEqual(h.store.writes(), [], 'NOT spoken, so NOT acked -- and nothing else was written');
  assert.equal(h.valueAt(`${OUTBOX}/r-1/acked`), null);
  // Once the speaker works, the next pass speaks it and acks it.
  h.speaker.setFailing(false);
  const again = await h.client.poll();
  assert.deepEqual(again.spoken, ['r-1']);
  assert.deepEqual(again.acked, ['r-1']);
});

test('it does not speak or ack the same message twice', async () => {
  const h = await harness();
  await plantOutbox(h, 'r-1');
  await h.client.poll();
  const second = await h.client.poll();
  assert.deepEqual(h.speaker.spoken(), ['run 42 finished']);
  assert.deepEqual(second.spoken, []);
  assert.deepEqual(second.acked, []);
  assert.equal(h.store.writes().length, 1, 'the ack is written once');
  assert.equal(second.presence.reason, PRESENCE_REASON.NO_HEARTBEAT);
});

test('two polls in flight together speak the message once, not twice', async () => {
  // The ack is written only AFTER the speak (the plan's order), so a poll that
  // passes while another poll is still running reads the same message unacked
  // and would speak it again -- the owner hears the sentence twice, which is
  // the thing the order exists to prevent. A screen that polls on a timer and
  // again on a reconnect does exactly that without meaning to, so the client
  // serialises its own passes rather than documenting the duty away.
  const h = await harness();
  await plantOutbox(h, 'r-1');
  const [first, second] = await Promise.all([h.client.poll(), h.client.poll()]);
  assert.deepEqual(h.speaker.spoken(), ['run 42 finished'], 'spoken once, for the owner to hear once');
  assert.deepEqual(first.spoken, ['r-1']);
  assert.deepEqual(second.spoken, [], 'the second pass begins after the first has acked');
  assert.equal(
    h.store.writes().filter(({ path }) => path === `${OUTBOX}/r-1/acked`).length,
    1,
    'and the ack is written once, not twice',
  );
  assertD113WriteSet(h.store, h.paths);
});

test('an ack that did not land is retried, and the message is not spoken a second time', async () => {
  // The reason the phone remembers what it has spoken: the speak succeeded and
  // the ACK write did not (the phone went offline between the two). A message
  // the owner has already heard must not be read out again on the next pass --
  // that is a bug in the room, not a retry -- while the ack is still owed.
  const h = await harness();
  await plantOutbox(h, 'r-1');
  h.store.db.setFailures({ failWrites: true });
  const first = await h.client.poll();
  assert.deepEqual(first.spoken, ['r-1']);
  assert.deepEqual(first.ackFailed, [{ id: 'r-1', code: STORE_CODES.WRITE_FAILED }]);
  assert.deepEqual(h.speaker.spoken(), ['run 42 finished']);

  h.store.db.setFailures({ failWrites: false });
  const second = await h.client.poll();
  assert.deepEqual(h.speaker.spoken(), ['run 42 finished'], 'spoken once, for the owner to hear once');
  assert.deepEqual(second.spoken, [], 'and not announced as spoken again');
  assert.deepEqual(second.acked, ['r-1'], 'but the ack is still owed, and is paid');
  assert.equal(h.valueAt(`${OUTBOX}/r-1/acked`), true);
  assertD113WriteSet(h.store, h.paths);
});

test('a message this phone acked in an earlier run is not spoken again after a restart', async () => {
  // A restart has no memory: the only evidence the message was spoken is the
  // `acked` child the phone itself wrote, which is what it is for.
  const h = await harness();
  await plantOutbox(h, 'r-1');
  await h.client.poll();
  const restarted = await harness({ db: h.store.db });
  const report = await restarted.client.poll();
  assert.deepEqual(report.spoken, []);
  assert.deepEqual(report.acked, []);
  assert.deepEqual(restarted.speaker.spoken(), []);
  assert.deepEqual(restarted.store.writes(), []);
});

test('a send whose message is already there is landed, even after a restart', async () => {
  // The lost-acknowledgement case across a restart: the write landed, the app
  // never learned so, and the next run asks again for the same id. The rules
  // deny the re-write (`!data.exists()`), and the phone must read that as
  // "it is there" rather than as a failure -- otherwise the utterance stays
  // "unsent" for ever while the consumer is already acting on it.
  const h = await harness();
  await h.client.send('run 42 finished');
  const first = h.valueAt(`${INBOX}/m-1`);
  const restarted = await harness({ db: h.store.db, newId: () => 'm-1' });
  const sent = await restarted.client.send('run 42 finished');
  assert.deepEqual(sent, { ok: true, id: 'm-1', at: T0 });
  assert.deepEqual(restarted.client.pending(), [
    { id: 'm-1', at: T0, text: 'run 42 finished', landed: true },
  ]);
  assert.deepEqual(restarted.valueAt(`${INBOX}/m-1`), first, 'and the first message stands');
});

test('messages are spoken oldest first, and an empty outbox says so', async () => {
  const h = await harness();
  await plantOutbox(h, 'r-2', { text: 'second', at: T0 - 1_000 });
  await plantOutbox(h, 'r-1', { text: 'first', at: T0 - 5_000 });
  await h.client.poll();
  assert.deepEqual(h.speaker.spoken(), ['first', 'second']);
  const empty = await harness();
  const report = await empty.client.poll();
  assert.deepEqual(report.spoken, []);
  assert.deepEqual(report.acked, []);
  assert.deepEqual(empty.store.writes(), []);
});

// ---------------------------------------------------------------------------
// What does not verify is dropped
// ---------------------------------------------------------------------------

test('a message in a node named `__proto__` is read, spoken and acked like any other', async () => {
  // `__proto__` is a legal database key, and the database hands a subtree back
  // with it as an OWN key (a listener's value is what `JSON.parse` builds). The
  // double must deliver it the same way: built with plain assignment the node
  // would vanish from `Object.entries`, and the phone would never speak a
  // message the consumer had answered -- exactly the kind of difference a fake
  // written to hold this client's rules must not have.
  const h = await harness();
  await plantOutbox(h, '__proto__');
  const report = await h.client.poll();
  assert.deepEqual(report.dropped, []);
  assert.deepEqual(report.spoken, ['__proto__']);
  assert.deepEqual(h.speaker.spoken(), ['run 42 finished']);
  assert.equal(h.valueAt(`${OUTBOX}/__proto__/acked`), true, 'and it is acked on its own child');
  assert.equal({}.id, undefined, 'with nothing written onto Object.prototype');
  assertD113WriteSet(h.store, h.paths);
});

test('a message whose id is `acked` is a message in both directions', async () => {
  // The template's `acked` rule is scoped to `outbox/$msg/acked`, and `$msg` is
  // any database key -- so `acked` is a legal id for an inbox message this phone
  // writes and for an outbox message it reads. A double that classified by
  // suffix answered about neither, and this is that difference at the client's
  // own boundary: the same id, written and spoken, with D113's write set intact.
  const sent = await harness({ newId: () => 'acked' });
  assert.deepEqual(await sent.client.send('run 42 finished'), { ok: true, id: 'acked', at: T0 });
  assert.equal(sent.valueAt(`${INBOX}/acked`).id, 'acked', 'the message lands under its own id');
  assertD113WriteSet(sent.store, sent.paths);

  const read = await harness();
  await plantOutbox(read, 'acked');
  const report = await read.client.poll();
  assert.deepEqual(report.dropped, []);
  assert.deepEqual(report.spoken, ['acked']);
  assert.deepEqual(read.speaker.spoken(), ['run 42 finished']);
  assert.equal(read.valueAt(`${OUTBOX}/acked/acked`), true, 'and it is acked on its own child');
  assertD113WriteSet(read.store, read.paths);
});

test('a tampered text, id or timestamp is dropped, never spoken and never acked', async () => {
  // The plan's tension (b) resolution: the phone verifies the tag over each
  // outbox message before it speaks a word or acks. Each tamper happens AFTER
  // the signature, so what arrives is a record whose tag no longer covers it.
  // (`acked` is deliberately not on this list: `canonicalBytes` omits it, which
  // `mac.test.js` asserts from the other side. A tampered `sender` is not on it
  // either: that is the template's `sender == $mb`, refused before the tag, and
  // it has its own test below.)
  for (const [what, fields, tamper, expected] of [
    ['text', {}, (record) => ({ ...record, text: 'transfer the money' }), CLIENT_CODES.FORGED],
    ['at', {}, (record) => ({ ...record, at: record.at + 1 }), CLIENT_CODES.FORGED],
    // Every other field a message can carry is covered the same way. The
    // exhaustive list -- including the fields that are outside the bytes --
    // is `mac.test.js`'s; these are the client's samples of it.
    ['agent', { agent: 'orch-1' }, (record) => ({ ...record, agent: 'orch-2' }), CLIENT_CODES.FORGED],
    [
      'options',
      { kind: 'prompt', options: ['one'] },
      (record) => ({ ...record, options: ['two'] }),
      CLIENT_CODES.FORGED,
    ],
    [
      'reply_to',
      { kind: 'answer', reply_to: 'm-1' },
      (record) => ({ ...record, reply_to: 'm-2' }),
      CLIENT_CODES.FORGED,
    ],
    // A changed `id` is refused one step earlier, and better: the node's key
    // and the record's id disagree, which is the shape a renamed write has.
    ['id', {}, (record) => ({ ...record, id: 'r-other' }), CLIENT_CODES.MISMATCHED],
  ]) {
    const h = await harness();
    await plantOutbox(h, 'r-1', fields, { tamper });
    const report = await h.client.poll();
    assert.deepEqual(h.speaker.spoken(), [], what);
    assert.deepEqual(report.spoken, [], what);
    assert.deepEqual(report.acked, [], what);
    assert.deepEqual(h.store.writes(), [], what);
    assert.equal(report.dropped.length, 1, what);
    assert.equal(report.dropped[0].id, 'r-1', what);
    assert.equal(report.dropped[0].code, expected, what);
  }
});

test('a message with no tag, and a tag of the wrong shape, are dropped', async () => {
  const h = await harness();
  const record = await plantOutbox(h, 'r-1', {}, { tamper: ({ mac: _gone, ...rest }) => rest });
  const untaggedReport = await h.client.poll();
  assert.deepEqual(untaggedReport.spoken, []);
  assert.equal(untaggedReport.dropped.length, 1);
  assert.equal(untaggedReport.dropped[0].id, 'r-1');
  assert.equal(
    untaggedReport.dropped[0].code,
    validateMessage(record).code,
    'the schema\'s own refusal, verbatim',
  );
  assert.deepEqual(h.store.writes(), [], 'never spoken, never acked');

  // The other half of the title: a tag that is there and is not the schema's
  // SHAPE -- 64 lowercase hex characters, one tag in one encoding. A record
  // like that is not a message at all, so it is dropped with the schema's own
  // code and with the field the schema blames, before its tag is consulted:
  // the difference between "no tag" and "not a tag" is the schema's, and this
  // module does not add a second rule for it.
  for (const mac of ['f', 'F'.repeat(64), 'z'.repeat(64), 'f'.repeat(63), 'f'.repeat(65), 42, null, '']) {
    const other = await harness();
    await plantOutbox(other, 'r-1', {}, { tamper: (record) => ({ ...record, mac }) });
    const shapeReport = await other.client.poll();
    assert.deepEqual(shapeReport.spoken, [], String(mac));
    assert.equal(shapeReport.dropped.length, 1, String(mac));
    assert.equal(shapeReport.dropped[0].id, 'r-1', String(mac));
    assert.equal(shapeReport.dropped[0].field, 'mac', String(mac));
    assert.ok(
      [SCHEMA_REFUSALS.BAD_MAC, SCHEMA_REFUSALS.WRONG_TYPE, SCHEMA_REFUSALS.EMPTY_STRING]
        .includes(shapeReport.dropped[0].code),
      `${JSON.stringify(mac)}: the schema's own refusal, not this module's`,
    );
    assert.deepEqual(other.store.writes(), [], String(mac));
  }
});

test('a tag made for another node, or with another key, does not verify', async () => {
  // One key signs all three directions, and the direction is inside the bytes
  // (the owner's ruling of 2026-10-18): a message reflected from the heartbeat
  // node into the outbox, or re-signed by another pairing, fails.
  const wrongDirection = await harness();
  await plantOutbox(wrongDirection, 'r-1', {}, { direction: 'inbox' });
  const directionReport = await wrongDirection.client.poll();
  assert.deepEqual(directionReport.spoken, []);
  assert.deepEqual(directionReport.dropped, [{ id: 'r-1', code: CLIENT_CODES.FORGED, field: 'mac' }]);

  const wrongKey = await harness();
  await plantOutbox(wrongKey, 'r-1', {}, { key: OTHER_KEY });
  const keyReport = await wrongKey.client.poll();
  assert.deepEqual(keyReport.spoken, []);
  assert.deepEqual(keyReport.dropped, [{ id: 'r-1', code: CLIENT_CODES.FORGED, field: 'mac' }]);
  assert.deepEqual(wrongKey.store.writes(), []);

  // ... and a record signed by THIS pairing, in THIS direction, is spoken: the
  // two drops above are about the tag and nothing else.
  const right = await harness();
  await plantOutbox(right, 'r-1');
  assert.deepEqual((await right.client.poll()).spoken, ['r-1']);
});

test('a record whose key and id disagree, or whose sender is another mailbox, is dropped', async () => {
  const mismatched = await harness();
  // The template holds the node's key and the record's id equal; a node where
  // they disagree is the shape a spliced or renamed write has.
  const record = signed({ id: 'r-other', sender: UID, text: 'run 42 finished', at: T0 - 1_000 }, 'outbox');
  await mismatched.db.write(`${OUTBOX}/r-1`, record);
  const mismatchReport = await mismatched.client.poll();
  assert.deepEqual(mismatchReport.dropped, [{ id: 'r-1', code: CLIENT_CODES.MISMATCHED, field: 'id' }]);
  assert.deepEqual(mismatched.speaker.spoken(), []);

  // The sender is checked here too, rather than trusted to the rules: the
  // template's `sender == $mb` is P2.1's, and the phone is the reader.
  const foreign = await harness();
  await foreign.db.write(`${OUTBOX}/r-1`, signed({
    id: 'r-1',
    sender: 'SomeoneElsesUid00000000',
    text: 'run 42 finished',
    at: T0 - 1_000,
  }, 'outbox'));
  const foreignReport = await foreign.client.poll();
  assert.deepEqual(foreignReport.dropped, [{ id: 'r-1', code: CLIENT_CODES.WRONG_SENDER, field: 'sender' }]);
  assert.deepEqual(foreign.speaker.spoken(), []);
  assert.deepEqual(foreign.store.writes(), []);
});

test('an unreadable outbox is a refusal, and nothing is spoken', async () => {
  const h = await harness();
  await plantOutbox(h, 'r-1');
  h.store.db.setFailures({ failReads: true });
  assert.deepEqual(await h.client.poll(), refuse(STORE_CODES.READ_FAILED, 'outbox'));
  assert.deepEqual(h.speaker.spoken(), []);
  assert.deepEqual(h.store.writes(), []);
});

// ---------------------------------------------------------------------------
// Presence: read, verified, never live on a tag nobody can trust
// ---------------------------------------------------------------------------

test('a signed heartbeat is read through P1.4 and its agent list is carried', async () => {
  const h = await harness();
  await plantHeartbeat(h, { agents: [{ id: 'orch-1', name: 'orchestrator' }] });
  const report = await h.client.poll();
  assert.deepEqual(report.presence, {
    state: STATE.LIVE,
    verified: true,
    reason: REASON.FRESH,
    ageMs: 1_000,
    agents: [{ id: 'orch-1', name: 'orchestrator' }],
  });
  // A heartbeat that is older than three intervals is stale, and still trusted.
  h.advance(5 * INTERVAL_MS);
  const stale = await h.client.poll();
  assert.equal(stale.presence.state, STATE.STALE);
  assert.equal(stale.presence.reason, REASON.MISSED_INTERVALS);
  assert.equal(stale.presence.verified, true);
  // Far older is offline: a long silence is not a stale one.
  h.advance(20 * INTERVAL_MS);
  const offline = await h.client.poll();
  assert.equal(offline.presence.state, STATE.OFFLINE);
  assert.equal(offline.presence.reason, REASON.LONG_SILENCE);
});

test('an unsigned heartbeat, or one signed for another node, is offline with no agents', async () => {
  // The plan's sentence: "an unsigned heartbeat is treated as offline". An
  // orchestrator that could announce itself without a tag could announce
  // itself as the agent the owner talks to -- which is why the tag covers the
  // agent list too.
  const AGENTS = [{ id: 'orch-1', name: 'orchestrator' }];
  const unsigned = await harness();
  // A heartbeat whose tag is there and wrong: the schema accepts the shape, so
  // the refusal is this client's (`heartbeat-unsigned`), not the schema's.
  await plantHeartbeat(unsigned, { agents: AGENTS }, { raw: { at: T0 - 1_000, boot: 'boot-0001', agents: AGENTS, mac: 'f'.repeat(64) } });
  const unsignedReport = await unsigned.client.poll();
  assert.deepEqual(unsignedReport.presence, {
    state: STATE.OFFLINE,
    verified: false,
    reason: PRESENCE_REASON.UNSIGNED,
    agents: null,
    ageMs: null,
  });
  // ... and one with no tag at all is not a heartbeat: the schema refuses its
  // shape, before any tag is looked for.
  const tagless = await harness();
  await plantHeartbeat(tagless, { agents: AGENTS }, { raw: { at: T0 - 1_000, boot: 'boot-0001', agents: AGENTS } });
  assert.equal((await tagless.client.poll()).presence.reason, PRESENCE_REASON.INVALID);

  const reflected = await harness();
  await plantHeartbeat(reflected, {}, { direction: 'inbox' });
  assert.equal((await reflected.client.poll()).presence.reason, PRESENCE_REASON.UNSIGNED);

  // A heartbeat whose `at` was moved after it was signed: the tag no longer
  // covers the record, so it is worth nothing -- an old-but-honest heartbeat
  // and a rewritten one must not read the same.
  const tampered = await harness();
  const honest = signed({ at: T0 - 1_000, boot: 'boot-0001' }, 'heartbeat');
  await plantHeartbeat(tampered, {}, { raw: { ...honest, at: T0 - 5_000_000 } });
  const tamperedReport = await tampered.client.poll();
  assert.equal(tamperedReport.presence.state, STATE.OFFLINE);
  assert.equal(tamperedReport.presence.verified, false);
  assert.equal(tamperedReport.presence.reason, PRESENCE_REASON.UNSIGNED);
  assert.equal(tamperedReport.presence.agents, null);
});

test('an empty heartbeat node is offline, and garbage is offline too', async () => {
  const empty = await harness();
  assert.deepEqual((await empty.client.poll()).presence, {
    state: STATE.OFFLINE,
    verified: false,
    reason: PRESENCE_REASON.NO_HEARTBEAT,
    agents: null,
    ageMs: null,
  });
  const garbage = await harness();
  await garbage.db.write(HEARTBEAT, { boot: 'boot-0001' });
  const garbageReport = await garbage.client.poll();
  assert.equal(garbageReport.presence.reason, PRESENCE_REASON.INVALID);
  assert.equal(garbageReport.presence.state, STATE.OFFLINE);
  assert.equal(garbageReport.presence.verified, false);
});

test('a signed heartbeat P1.4 will not judge is offline for this module\'s own reason', async () => {
  // The branch `presenceOf` called unreachable, which IS reachable -- and this
  // is the case that reaches it. `at: 0` is **schema-legal** (`timestampField`
  // refuses a negative integer and not zero) and P1.4 refuses it as the shape
  // of an *unset counter*, so a heartbeat this client has already validated and
  // verified is still a refusal one call further on.
  //
  // The refusal is P1.4's (`invalid-heartbeat-timestamp`) and P1.4's own README
  // says a refusal is never coerced into a state -- neither is it one of this
  // module's presence reasons, so handing the raw code out as `reason` answers a
  // caller with a string neither `REASON` nor `PRESENCE_REASON` contains, which
  // is exactly what the presence contract at the top of `client.js` promises
  // cannot happen.
  const h = await harness();
  await plantHeartbeat(h, { at: 0 });
  // The premise, held to the two modules' own code rather than to this comment.
  assert.equal(
    validateHeartbeat({ at: 0, boot: 'boot-0001', mac: 'a'.repeat(64) }).ok,
    true,
    'the schema accepts `at: 0`',
  );
  const judged = evaluatePresence({ heartbeat: { at: 0, bootId: 'boot-0001' }, now: h.now(), intervalMs: INTERVAL_MS });
  assert.equal(judged.ok, false, 'P1.4 refuses it');
  assert.equal(judged.code, 'invalid-heartbeat-timestamp');
  // The tag verified, so the record is the orchestrator's and `verified` stays
  // true; the state is `offline` and never `live` (fail closed).
  assert.deepEqual((await h.client.poll()).presence, {
    state: STATE.OFFLINE,
    verified: true,
    reason: PRESENCE_REASON.REFUSED,
    agents: null,
    ageMs: null,
  });
  // The reason is this module's, and every reason it can answer with is one of
  // the two lists its contract names.
  const reasons = new Set([...Object.values(REASON), ...Object.values(PRESENCE_REASON)]);
  assert.equal(reasons.has(PRESENCE_REASON.REFUSED), true);
  assert.equal(reasons.has('invalid-heartbeat-timestamp'), false, 'P1.4\'s codes are not reasons');
});

test('an unreadable heartbeat does not stop the replies, and is never live', async () => {
  const h = await harness();
  await plantOutbox(h, 'r-1');
  // Only the heartbeat read fails: an outbox that can be read is spoken, and a
  // heartbeat nobody could read is not evidence that anything is running.
  const failing = {
    async write(path, value) {
      return h.store.write(path, value);
    },
    async read(path) {
      if (path === HEARTBEAT) return refuse(STORE_CODES.READ_FAILED, 'path');
      return h.store.read(path);
    },
  };
  const client = createMailboxClient({
    account: ACCOUNT,
    keys: h.keys,
    store: failing,
    connection: h.connection,
    speaker: h.speaker,
    now: h.now,
    newId: () => 'm-1',
    secrets: SECRETS,
    heartbeatIntervalMs: INTERVAL_MS,
  });
  const report = await client.poll();
  assert.deepEqual(report.spoken, ['r-1']);
  assert.deepEqual(report.acked, ['r-1']);
  assert.deepEqual(report.presence, {
    state: STATE.OFFLINE,
    verified: false,
    reason: PRESENCE_REASON.UNREADABLE,
    agents: null,
    ageMs: null,
  });
});

test('the one mapping between the schema\'s heartbeat and P1.4\'s rule is named and tested', async () => {
  // P1.4 reads `{at, bootId}`; the schema writes `{at, boot, agents?, mac}`.
  // The difference is recorded in `shared/mailbox-schema/README.md` as a
  // conflict for the owner, and this is the mapping P3.6 supplies until the
  // owner rules -- so that a signed heartbeat is not read as offline over a
  // field name.
  assert.deepEqual(toPresenceHeartbeat({ at: T0, boot: 'boot-0001' }), { at: T0, bootId: 'boot-0001' });
  // An object that is not a heartbeat is not "mapped with two undefined
  // fields": the function's contract says it throws for anything that is not an
  // object with both fields, and `boot` undefined would reach P1.4 as a
  // heartbeat with no boot id. (Over P1.4's rule the fields are the schema's,
  // which is where the two types come from.)
  for (const value of [
    null, 42, 'x', [],
    {},
    { at: T0 },
    { boot: 'boot-0001' },
    { at: T0, boot: 42 },
    { at: 'yesterday', boot: 'boot-0001' },
  ]) {
    assert.throws(() => toPresenceHeartbeat(value), TypeError, JSON.stringify(value) ?? String(value));
  }
  // P1.4 refuses the schema's own fields, which is the conflict in one line…
  const validated = { at: T0 - 1_000, boot: 'boot-0001', mac: 'a'.repeat(64) };
  const direct = evaluatePresence({ heartbeat: validated, now: T0, intervalMs: INTERVAL_MS });
  assert.equal(direct.ok, false, 'P1.4 does not read `boot`');
  assert.equal(direct.code, 'heartbeat-unknown-field');
  // … and the mapping is what makes the same heartbeat readable.
  const mapped = evaluatePresence({
    heartbeat: toPresenceHeartbeat(validated),
    now: T0,
    intervalMs: INTERVAL_MS,
  });
  assert.equal(mapped.ok, true);
  assert.equal(mapped.state, STATE.LIVE);
  assert.equal(mapped.bootId, 'boot-0001');
});

// ---------------------------------------------------------------------------
// The reconnect, end to end, against P1.1's own ledger
// ---------------------------------------------------------------------------

test('an unacked message re-delivered after a reconnect is a duplicate P1.1 does not act on twice', async () => {
  // The plan's sentence, with P1.1 as the consumer's ledger rather than a
  // restatement of it: the phone's half is that the re-send is the SAME
  // record, and P1.1's half is that the same record is a duplicate.
  const h = await harness();
  h.store.db.setFailures({ failWrites: true });
  await h.client.send('run 42 finished');
  h.store.db.setFailures({ failWrites: false });
  h.advance(60_000);
  await h.client.flush();
  const said = h.valueAt(`${INBOX}/m-1`);

  const opened = initialState({ now: h.now(), promptTtlMs: MAILBOX_TTL_MS });
  assert.equal(opened.ok, true);
  const first = step(opened.state, { type: 'deliver', now: h.now(), message: said, kind: KIND.COMMAND });
  assert.equal(first.outcome, OUTCOME.ACCEPTED);
  const acted = step(first.state, { type: 'act', now: h.now() });
  assert.equal(acted.outcome, OUTCOME.ACTED);

  // The outbox has not answered, so the phone re-sends on the next pass...
  h.advance(60_000);
  assert.deepEqual((await h.client.flush()).written, ['m-1']);
  assert.deepEqual(h.store.writes().map((entry) => entry.path), [`${INBOX}/m-1`, `${INBOX}/m-1`, `${INBOX}/m-1`]);
  assert.deepEqual(h.store.writes()[1].value, said, 'the same id, the same `at`, the same tag');
  assert.deepEqual(h.store.writes()[2].value, said, 'and again, the same record');
  // ... and the consumer, handed it again, answers `duplicate` and acts once.
  const again = step(acted.state, { type: 'deliver', now: h.now(), message: said, kind: KIND.COMMAND });
  assert.equal(again.outcome, OUTCOME.DUPLICATE);
  const secondAct = step(again.state, { type: 'act', now: h.now() });
  assert.equal(secondAct.outcome, OUTCOME.NOTHING_TO_ACT);

  // The counterfactual, and the reason the re-send must be verbatim: a client
  // that re-stamped the message would not be answered as a duplicate at all.
  const restamped = { ...said, at: said.at + 1, mac: macOf({ ...said, at: said.at + 1 }, MAC_KEY, 'inbox') };
  const conflicted = step(acted.state, { type: 'deliver', now: h.now(), message: restamped, kind: KIND.COMMAND });
  assert.equal(conflicted.ok, false);
  assert.equal(conflicted.code, REFUSAL.ID_CONFLICT);
});

test('a verified reply stops the phone re-delivering; an unverified one does not', async () => {
  const h = await harness();
  await h.client.send('run 42 finished');
  assert.equal(h.client.pending().length, 1);
  // A reply the pairing signed answers the message it names (`reply_to`), so
  // there is nothing left to re-deliver.
  await plantOutbox(h, 'r-1', { kind: 'answer', reply_to: 'm-1', text: 'done' });
  const report = await h.client.poll();
  assert.deepEqual(report.spoken, ['r-1']);
  assert.deepEqual(h.client.pending(), []);
  assert.deepEqual((await h.client.flush()).written, []);
  assertD113WriteSet(h.store, h.paths);

  // An unverified reply is not a way for a forger to make the phone give up.
  const forger = await harness();
  await forger.client.send('run 42 finished');
  await plantOutbox(
    forger,
    'r-1',
    { kind: 'answer', reply_to: 'm-1', text: 'done' },
    { tamper: (record) => ({ ...record, text: 'never mind' }) },
  );
  const forged = await forger.client.poll();
  assert.deepEqual(forged.dropped, [{ id: 'r-1', code: CLIENT_CODES.FORGED, field: 'mac' }]);
  assert.equal(forger.client.pending().length, 1, 'still being delivered');
});

// ---------------------------------------------------------------------------
// The write set, and the key
// ---------------------------------------------------------------------------

test('the whole write set of a full scenario is D113\'s two paths and nothing else', async () => {
  const h = await harness();
  // An utterance that lands, one that does not, a reconnect, a reply that is
  // spoken and acked, a tampered one that is dropped, and a heartbeat that is
  // read.
  await h.client.send('run 42 finished');
  h.store.db.setFailures({ failWrites: true });
  await h.client.send('and again');
  h.store.db.setFailures({ failWrites: false });
  await plantOutbox(h, 'r-1', { text: 'done' });
  await plantOutbox(h, 'r-2', { text: 'tampered', at: T0 - 2_000 }, {
    tamper: (record) => ({ ...record, text: 'changed' }),
  });
  await plantHeartbeat(h);
  h.advance(30_000);
  const report = await h.client.poll();
  // The poll re-delivers both outstanding messages -- the one that landed and
  // the one that never did; neither has been answered -- and the re-write of
  // the first is denied by the rules and read as landed.
  assert.deepEqual(report.written, ['m-1', 'm-2']);
  assert.deepEqual(report.spoken, ['r-1']);
  assert.deepEqual(report.acked, ['r-1']);
  assert.deepEqual(report.dropped, [{ id: 'r-2', code: CLIENT_CODES.FORGED, field: 'mac' }]);
  assert.equal(report.presence.state, STATE.LIVE);
  assertD113WriteSet(h.store, h.paths);
  // The heartbeat node itself is untouched, and the ack is the only thing
  // written anywhere near the outbox.
  assert.equal(h.store.writes().some((entry) => entry.path.includes('/outbox/r-') && !entry.path.endsWith('/acked')), false);
});

test('the key never appears in a write, a report, a refusal or a pending entry', async () => {
  const h = await harness();
  const sent = await h.client.send('run 42 finished');
  await plantOutbox(h, 'r-1');
  await plantHeartbeat(h);
  const report = await h.client.poll();
  const seen = JSON.stringify({
    sent,
    report,
    pending: h.client.pending(),
    writes: h.store.writes(),
    reads: h.store.reads(),
    spoken: h.speaker.spoken(),
    paths: h.paths,
  });
  assert.equal(seen.includes(MAC_KEY), false, 'the key is not in anything the client hands back');
  // ... nor in what it refused. An unpaired phone is the case that would carry
  // it if anything did.
  const unpaired = await harness({ pair: false });
  assert.equal(JSON.stringify(await unpaired.client.send('run 42 finished')).includes(MAC_KEY), false);
  // The store holds the tag and not the key: a tag is a digest, and the key is
  // not one of the bytes it covers.
  assert.equal(JSON.stringify(h.store.writes()).includes(MAC_KEY), false);
  // ... and no module in this directory prints anything at all (the boundary
  // scan asserts the same, over every file: `src/boundary.test.js`).
});

test('nothing in the module reads a clock, a random source, a file or a global', () => {
  // The client is handed `now` and `newId`, which is what makes every test in
  // this file a test of a decision: a `Date.now` or a `Math.random` anywhere in
  // the client would make "the same record is re-sent" depend on the machine,
  // and a `process`/`globalThis` read would make it depend on where it runs.
  //
  // The scan is over the CODE, with the comments out first (the same stripper
  // `src/boundary.test.js` uses, and for the same reason: this file's own
  // header names the tokens it forbids). The positive control runs first, so a
  // scan that had stopped matching anything cannot pass silently.
  const FORBIDDEN = [/\bDate\.now\b/, /\bMath\.random\b/, /\bnew Date\b/, /\bprocess\./, /\bglobalThis\b/, /\brequire\s*\(/];
  const control = `const t = Date.now();`;
  assert.equal(FORBIDDEN.some((pattern) => pattern.test(code(control))), true, 'the control is flagged');
  assert.equal(FORBIDDEN.some((pattern) => pattern.test(code(`// Date.now() is not read here`))), false, 'prose is not');

  for (const name of [
    'client.js',
    'paths.js',
    'mac.js',
    'sha256.js',
    'store.js',
    'speaker.js',
    'redact.js',
    'gate.js',
    'connection.js',
  ]) {
    const source = code(readFileSync(new URL(name, import.meta.url), 'utf8'));
    for (const pattern of FORBIDDEN) {
      assert.equal(pattern.test(source), false, `${name}: ${pattern}`);
    }
  }
});

/**
 * A file's code without its comments -- `src/boundary.test.js`'s function,
 * repeated here rather than imported, because a test may not depend on another
 * test's internals and this one is three lines.
 */
function code(text) {
  return text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');
}

// ---------------------------------------------------------------------------
// The offline gate (P6.10; D167, G21)
// ---------------------------------------------------------------------------
//
// The plan writes this task's verification as three clauses, and they are the
// three sections below:
//
//   * with no connection, or a stale/offline consumer, **nothing is written**
//     and **the user is told**;
//   * an **action record is never queued**;
//   * the client **never relies on the SDK's offline queue** -- a test that
//     pending writes are never issued.
//
// Everything here runs over a fake connection state (`MemoryConnection`) and a
// store whose SDK queue can be turned on (`MemoryMailboxStore.setOffline`), so
// the claims are about decisions and not about a network.

/** D169's action record, as P6.2's machine would hand one over. */
function actionRecord(at) {
  return {
    id: 'a-1',
    verb: 'cancel',
    target: { run: 'run-42' },
    confirmation: { id: 'c-1', at },
  };
}

test('with no connection nothing is written, and the owner is told why', async () => {
  const h = await harness();
  h.connection.setOnline(false);
  const refused = await h.client.send('cancel the docs refactor');
  assert.deepEqual(refused, {
    ok: false,
    code: GATE_CODES.NO_CONNECTION,
    field: 'connection',
    message: OFFLINE_SPEECH[GATE_CODES.NO_CONNECTION],
    told: true,
  });
  // Nothing was written, nothing was read, and no id was minted for it: the
  // gate is asked before `newId`, so a refused utterance cannot leave a hole in
  // the id sequence that looks like a lost message.
  assert.deepEqual(h.store.writes(), [], 'nothing was written');
  assert.deepEqual(h.store.reads(), [], 'nothing was read');
  // And the owner heard it. "Says why" is D167's own requirement, and the
  // speaker is the one port this directory has for saying anything.
  assert.deepEqual(h.speaker.spoken(), [OFFLINE_SPEECH[GATE_CODES.NO_CONNECTION]]);
  // D167's "keeps the utterance only as unsent text the user can resend": the
  // text is there, the REDACTED text, and it is not in the outbox queue.
  assert.deepEqual(h.client.unsent(), [
    { text: 'cancel the docs refactor', at: T0, code: GATE_CODES.NO_CONNECTION },
  ]);
  assert.deepEqual(h.client.pending(), [], 'unsent is not queued');
});

test('the connection is asked before the key is, and before anything is written', async () => {
  // The ordering is the guard rather than a detail: a gate that runs after the
  // tag is made has already read the pairing key for an utterance that was
  // never going to be sent, and a gate that runs after the write has already
  // handed the write to the SDK.
  const h = await harness({ keys: countingKeys(new SecretStore(new MemorySecureStore())), pair: false });
  await pair(h.keys, MAC_KEY);
  h.connection.setOnline(false);
  const refused = await h.client.send('run 42 finished');
  assert.equal(refused.code, GATE_CODES.NO_CONNECTION);
  assert.deepEqual(h.keys.asked(), [], 'the key was never asked for');
  assert.deepEqual(h.store.writes(), [], 'nothing was written');
  assert.deepEqual(h.connection.asked().slice(-1), [false], 'the connection was what was asked');
});

test('a speak that failed does not change the refusal, and says so', async () => {
  // The refusal's CODE is what a screen switches on, and it must not depend on
  // whether the owner happened to be listening. `told` reports that separately.
  const h = await harness();
  h.speaker.setFailing(true);
  h.connection.setOnline(false);
  const refused = await h.client.send('run 42 finished');
  assert.equal(refused.code, GATE_CODES.NO_CONNECTION);
  assert.equal(refused.told, false);
  assert.deepEqual(h.store.writes(), []);
  // The utterance is still kept: the owner can still resend it, and the text
  // screen (P6.9) shows it even though the speaker did not.
  assert.equal(h.client.unsent().length, 1);
});

test('flush writes nothing while the connection is down, and keeps what it has', async () => {
  // The second half of "nothing is written", and the one a reader is most
  // likely to miss: a re-send is a write too, and with no connection it goes
  // into the SDK's queue exactly like a first one.
  const h = await harness({ db: new MemoryMailboxStore({ failWrites: true }) });
  const sent = await h.client.send('run 42 finished');
  assert.equal(sent.ok, false, 'the write did not land');
  h.db.setFailures({ failWrites: false });
  const before = h.store.writes().length;
  h.connection.setOnline(false);
  const refused = await h.client.flush();
  assert.deepEqual(refused, { ok: false, code: GATE_CODES.NO_CONNECTION, field: 'connection' });
  assert.equal(h.store.writes().length, before, 'no write was attempted');
  assert.equal(h.client.pending().length, 1, 'the record is still there');
  // And with the connection back, the same record goes out, unchanged.
  h.connection.setOnline(true);
  const flushed = await h.client.flush();
  assert.deepEqual(flushed.written, ['m-1']);
  assert.equal(h.client.pending()[0].landed, true);
  assert.equal(h.store.writes().length, before + 1);
});

test('an offline poll does nothing at all, and hears nothing', async () => {
  // A poll with no connection would read the SDK's cache (and could believe a
  // heartbeat that stopped being written) and would ack a message it read out
  // of that cache. There is nothing it can do that is not wrong, so it does
  // nothing.
  const h = await harness();
  await plantOutbox(h, 'r-1');
  h.connection.setOnline(false);
  const refused = await h.client.poll();
  assert.deepEqual(refused, { ok: false, code: GATE_CODES.NO_CONNECTION, field: 'connection' });
  assert.deepEqual(h.store.reads(), [], 'nothing was read');
  assert.deepEqual(h.store.writes(), [], 'nothing was written');
  assert.deepEqual(h.speaker.spoken(), [], 'and nothing was spoken over the owner');
});

test('a consumer that has gone quiet since the last poll refuses the sentence', async () => {
  // The gate reads the LAST poll's heartbeat and judges it AGAIN, at the moment
  // the owner speaks. A verdict cached at poll time would say "live" for a
  // consumer that died five minutes ago, which is the sentence D167 is about.
  const h = await harness();
  await plantHeartbeat(h);
  const polled = await h.client.poll();
  assert.equal(polled.presence.state, STATE.LIVE);
  assert.equal((await h.client.send('run 42 finished')).ok, true, 'a live consumer is spoken to');
  h.advance(INTERVAL_MS * 5);
  const refused = await h.client.send('and another thing');
  assert.deepEqual(refused, {
    ok: false,
    code: GATE_CODES.CONSUMER_STALE,
    field: 'presence',
    message: OFFLINE_SPEECH[GATE_CODES.CONSUMER_STALE],
    told: true,
  });
  assert.equal(h.store.writes().length, 1, 'only the first utterance was written');
});

test('a consumer that is offline, or unsigned, or on another clock, refuses too', async () => {
  // P1.4's other three states, each through the client rather than through the
  // gate alone: the presence the client reports is the presence the gate reads.
  for (const [name, plant, code] of [
    ['silent', async (h) => { await plantHeartbeat(h); h.advance(INTERVAL_MS * 20); }, GATE_CODES.CONSUMER_OFFLINE],
    ['empty', async () => {}, GATE_CODES.CONSUMER_OFFLINE],
    ['unsigned', async (h) => { await plantHeartbeat(h, {}, { raw: { at: h.now() - 1_000, boot: 'boot-0001' } }); }, GATE_CODES.CONSUMER_OFFLINE],
    ['another node\'s', async (h) => { await plantHeartbeat(h, {}, { direction: 'inbox' }); }, GATE_CODES.CONSUMER_OFFLINE],
    ['ahead of the server', async (h) => { await plantHeartbeat(h, { at: h.now() + INTERVAL_MS * 5 }); }, GATE_CODES.CONSUMER_CLOCK_DIFFERS],
  ]) {
    const h = await harness();
    await plant(h);
    const polled = await h.client.poll();
    assert.equal(polled.presence.state === STATE.LIVE, false, name);
    const refused = await h.client.send('run 42 finished');
    assert.equal(refused.code, code, name);
    assert.deepEqual(h.store.writes(), [], name);
    assert.deepEqual(h.speaker.spoken(), [OFFLINE_SPEECH[code]], name);
  }
});

test('before anything has been observed the phone still sends, and that is named', async () => {
  // The one place the plan's two conditions do not reach: a phone that has not
  // polled has observed nothing, and `null` is not evidence that the consumer is
  // down. Inventing a refusal from it would make a freshly paired phone unable
  // to say its first sentence. `gate.js` names the limit; this test holds the
  // behaviour, because a later change to it should be a decision and not an
  // accident.
  const h = await harness();
  assert.equal((await h.client.send('run 42 finished')).ok, true, 'nothing observed yet');
  // ... and once a poll HAS looked, the same consumer refuses.
  await plantHeartbeat(h);
  await h.client.poll();
  h.advance(INTERVAL_MS * 20);
  assert.equal((await h.client.send('and another thing')).code, GATE_CODES.CONSUMER_OFFLINE);
});

test('an action record is written once and never queued', async () => {
  // D167: "a destructive action record is never queued at all". It is not in
  // the outbox, so `flush` cannot re-send it, `pending()` cannot offer it and
  // the next poll cannot retry it -- a destructive command that arrives late is
  // exactly what the gate exists to prevent, and a retry is the same arrival
  // with a longer fuse.
  const h = await harness();
  const sent = await h.client.send('cancel the docs refactor', { action: actionRecord(T0) });
  assert.deepEqual(sent, { ok: true, id: 'm-1', at: T0 });
  assert.deepEqual(h.store.writes().map((entry) => entry.path), [`${INBOX}/m-1`]);
  assert.equal(h.store.writes()[0].value.kind, 'action');
  assert.deepEqual(h.store.writes()[0].value.action, actionRecord(T0));
  assert.deepEqual(h.client.pending(), [], 'an action is not in the outbox queue');
  assert.deepEqual(h.client.unsent(), [], 'and it is not unsent text either');
  await h.client.flush();
  assert.equal(h.store.writes().length, 1, 'flush never re-sends an action');
});

test('an action whose write did not land is dropped, not retried', async () => {
  // The same rule from the other side: an action that reaches the store and is
  // refused is REPORTED and forgotten. The owner confirms again (P6.2), which
  // is a new action id and a new confirmation -- not a silent retry of this one.
  const h = await harness({ db: new MemoryMailboxStore({ failWrites: true }) });
  const refused = await h.client.send('cancel the docs refactor', { action: actionRecord(T0) });
  assert.equal(refused.ok, false);
  assert.deepEqual(h.client.pending(), []);
  h.db.setFailures({ failWrites: false });
  await h.client.flush();
  assert.equal(h.store.writes().length, 1, 'the failed action was not re-sent');
});

test('a gated action is not kept as unsent text either', async () => {
  // The two rules meet here. With no connection the action is refused BEFORE
  // anything is written -- and it is not offered back as text to resend, because
  // resending a destructive command means confirming it again.
  const h = await harness();
  h.connection.setOnline(false);
  const refused = await h.client.send('cancel the docs refactor', { action: actionRecord(T0) });
  assert.equal(refused.code, GATE_CODES.NO_CONNECTION);
  assert.equal(refused.told, true, 'the owner is still told');
  assert.deepEqual(h.client.unsent(), [], 'but nothing is kept');
  assert.deepEqual(h.client.pending(), []);
  assert.deepEqual(h.store.writes(), []);
});

test('the client never relies on the SDK\'s offline queue: nothing is ever issued', async () => {
  // G21's hazard made concrete. The store here is the SDK: with an offline flag
  // set it does not refuse a write, it HOLDS it and resolves it as though it had
  // landed. The gate's job is that the client never hands it anything -- and the
  // test is worth nothing unless the queue would have noticed, so the positive
  // control comes first.
  const h = await harness();
  h.db.setOffline(true);
  await h.db.write(`${INBOX}/handed-over`, { any: 'value' });
  assert.equal(h.db.pendingWrites().length, 1, 'the queue does notice a write it is given');
  assert.equal(h.db.cancelPending(), 1);

  h.connection.setOnline(false);
  const refused = await h.client.send('cancel the docs refactor', { action: actionRecord(T0) });
  assert.equal(refused.code, GATE_CODES.NO_CONNECTION);
  assert.deepEqual(h.store.writes(), [], 'no write was issued');
  assert.deepEqual(h.db.pendingWrites(), [], 'and the SDK is holding nothing of ours');
  // The reconnect, spelled out: had a write been handed over, this is where it
  // would have run -- hours later, at a time nobody chose. It is empty.
  h.db.setOffline(false);
  assert.deepEqual(h.db.flushQueue(), { applied: [], refused: [] });
  assert.equal(h.valueAt(`${INBOX}/m-1`), null);
  assert.equal(h.valueAt(`${INBOX}/handed-over`), null);
});

test('a sentence comes from the gate alone, and every other refusal is bare', async () => {
  // README's bullet tells the screens (P6.9's text screen, P6.12's voice screen)
  // which refusal they may render. The claim was wrong twice: first it said
  // `flush` and `poll` carry a sentence too, then it said every refused `send`
  // does -- and the second is the worse one, because a screen following it
  // renders `undefined` on exactly the refusals it meets most (`bad-text`,
  // `too-long`, the redactor's, the key's, the store's). So all three shapes are
  // asserted here, and the `message` key's ABSENCE is the assertion.
  const h = await harness();
  h.connection.setOnline(false);

  const gated = await h.client.send('cancel the docs refactor');
  assert.equal(gated.message, OFFLINE_SPEECH[GATE_CODES.NO_CONNECTION]);
  assert.equal(typeof gated.told, 'boolean', 'a gated `send` says whether it was heard');

  // Still no connection: these two are the gate's refusals, and the gate has no
  // sentence for a read or a re-send.
  const whileOffline = [
    ['flush', await h.client.flush()],
    ['poll', await h.client.poll()],
  ];

  // The connection comes back, so this one is a refusal the gate did NOT make --
  // an ordinary failure of an ordinary send, and the shape the screens meet most.
  h.connection.setOnline(true);
  const tooLong = await h.client.send('x'.repeat(4097));

  for (const [what, refused] of [...whileOffline, ['a `send` the gate did not refuse', tooLong]]) {
    assert.equal(refused.ok, false, `a refused ${what} is a refusal`);
    assert.deepEqual(
      Object.keys(refused).sort(),
      ['code', 'field', 'ok'],
      `nothing to render and nothing to speak on ${what}: no \`message\`, no \`told\``,
    );
    assert.equal(refused.message, undefined, `no sentence on ${what}`);
  }
  // And it is still refused for its own reason: the bare shape is not one code.
  assert.equal(tooLong.code, CLIENT_CODES.TOO_LONG, 'and it is the refusal it says it is');
  assert.equal(whileOffline[0][1].code, GATE_CODES.NO_CONNECTION);
});
