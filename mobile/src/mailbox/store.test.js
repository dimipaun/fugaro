// The port's double, held to the two rules it claims to model (P3.6).
//
// A fake is only worth the tests that trust it. `store.js` claims two things
// beyond "a map": that a second write of an existing node is refused the way
// `rules.json.tmpl`'s `!data.exists()` refuses it, and that an ack needs its
// message to be there (`data.parent().exists()`). The client's offline and
// re-send tests are written against both, so if the fake were permissive the
// client's own tests would prove less than they say. Each claim is asserted
// here, next to the switch that turns it off.

import assert from 'node:assert/strict';
import test from 'node:test';

import { isDatabaseKey } from './paths.js';
import { MemoryMailboxStore, STORE_CODES } from './store.js';

/** A mailbox id shaped like a uid, and never a real one. */
const UID = 'AbCdEf0123456789ghIJkl';
const INBOX = `mailbox/${UID}/inbox`;
const OUTBOX = `mailbox/${UID}/outbox`;

const message = (id) => ({ id, sender: UID, text: 'run 42 finished', at: 1_700_000_000_000, mac: 'f'.repeat(64) });

test('a write lands, reads back, and is recorded', async () => {
  const store = new MemoryMailboxStore();
  assert.deepEqual(await store.write(`${INBOX}/m-1`, message('m-1')), { ok: true });
  assert.deepEqual(await store.read(`${INBOX}/m-1`), { ok: true, value: message('m-1') });
  assert.deepEqual(store.writes(), [{ path: `${INBOX}/m-1`, value: message('m-1') }]);
  assert.deepEqual(store.reads(), [{ path: `${INBOX}/m-1` }]);
});

test('a second write of a node that exists is refused, and changes nothing', async () => {
  // The template writes the inbox node with `MAILBOX && newData.exists() &&
  // !data.exists()`: a re-send of a message that landed is DENIED. The denial
  // is recorded as an attempt, because the client's report is about what it
  // tried.
  const store = new MemoryMailboxStore();
  await store.write(`${INBOX}/m-1`, message('m-1'));
  const again = await store.write(`${INBOX}/m-1`, { ...message('m-1'), text: 'something else' });
  assert.equal(again.ok, false);
  assert.equal(again.code, STORE_CODES.EXISTS);
  assert.equal(again.field, 'path');
  assert.equal(store.valueAt(`${INBOX}/m-1`).text, 'run 42 finished', 'the first write stands');
  assert.equal(store.writes().length, 2, 'both attempts are recorded');
  // ... and the switch that models a database which quietly accepted it, which
  // is what shows the client does not depend on the refusal.
  const permissive = new MemoryMailboxStore({ existsRefuses: false });
  await permissive.write(`${INBOX}/m-1`, message('m-1'));
  assert.deepEqual(await permissive.write(`${INBOX}/m-1`, message('m-1')), { ok: true });
});

test('an ack needs its message to be there, and must be exactly true', async () => {
  // The template's `acked` child: `MAILBOX && newData.exists() &&
  // !data.exists()` with `newData.isBoolean() && newData.val() == true &&
  // data.parent().exists()`.
  const store = new MemoryMailboxStore();
  const ack = `${OUTBOX}/r-1/acked`;
  const orphan = await store.write(ack, true);
  assert.equal(orphan.code, STORE_CODES.NO_PARENT, 'no message, no ack');
  assert.equal(store.valueAt(ack), null, 'nothing was written');
  await store.write(`${OUTBOX}/r-1`, message('r-1'));
  for (const value of [false, 'true', 1, null, undefined]) {
    const refused = await store.write(ack, value);
    assert.equal(refused.code, STORE_CODES.BAD_VALUE, String(value));
  }
  assert.deepEqual(await store.write(ack, true), { ok: true });
  assert.equal(store.valueAt(ack), true);
  // ... and a second ack is refused the way the second message write is.
  assert.equal((await store.write(ack, true)).code, STORE_CODES.EXISTS);
});

test('only a message\'s own `acked` child is an ack: an id of `acked` is a message', async () => {
  // The template puts the `acked` rule at `mailbox/$mb/outbox/$msg/acked` and
  // nowhere else, and `$msg` is any database key -- so `acked` is a legal
  // MESSAGE ID in both directions. A double that classifies by suffix alone
  // refuses a legal inbox message (`bad-value`) and cannot plant a legal
  // outbox one (`no-parent`), which is a database the rules do not describe:
  // the client's own tests would then never see this id at all.
  const store = new MemoryMailboxStore();
  await store.write(`${INBOX}/acked`, message('acked'));
  assert.deepEqual(store.valueAt(`${INBOX}/acked`), message('acked'), 'an inbox message whose id is `acked`');
  await store.write(`${OUTBOX}/acked`, message('acked'));
  assert.deepEqual(store.valueAt(`${OUTBOX}/acked`), message('acked'), 'an outbox message whose id is `acked`');
  // ... and its own `acked` child is an ack like any other: `true`, with the
  // message under it.
  assert.deepEqual(await store.write(`${OUTBOX}/acked/acked`, true), { ok: true });
  assert.equal(store.valueAt(`${OUTBOX}/acked/acked`), true);
  assert.deepEqual(await store.read(OUTBOX), {
    ok: true,
    value: { acked: { ...message('acked'), acked: true } },
  });
  // The ack's own two rules still apply to it, whatever the id is.
  const other = new MemoryMailboxStore();
  assert.equal(
    (await other.write(`${OUTBOX}/acked/acked`, true)).code,
    STORE_CODES.NO_PARENT,
    'no message, no ack -- whichever id it has',
  );
  await other.write(`${OUTBOX}/acked`, message('acked'));
  assert.equal((await other.write(`${OUTBOX}/acked/acked`, false)).code, STORE_CODES.BAD_VALUE);
  // A deeper `acked` is not the template's ack at all: its rule is at
  // `outbox/$msg/acked` and not at any depth, so it is written as an ordinary
  // node (no `true` requirement and no parent requirement).
  assert.deepEqual(await other.write(`${OUTBOX}/acked/extra/acked`, { at: 1 }), { ok: true });
  assert.deepEqual(other.valueAt(`${OUTBOX}/acked/extra/acked`), { at: 1 });
});

test('a read of a subtree is a map of id to message, and null when it is empty', async () => {
  // The shape a Realtime Database listener hands back, which is what the
  // client's outbox reader is written against.
  const store = new MemoryMailboxStore();
  assert.deepEqual(await store.read(OUTBOX), { ok: true, value: null });
  await store.write(`${OUTBOX}/r-1`, message('r-1'));
  await store.write(`${OUTBOX}/r-2`, message('r-2'));
  await store.write(`${OUTBOX}/r-2/acked`, true);
  assert.deepEqual(await store.read(OUTBOX), {
    ok: true,
    value: { 'r-1': message('r-1'), 'r-2': { ...message('r-2'), acked: true } },
  });
  // A read is a value, not a reference into the store: mutating what came back
  // must not change what is stored.
  const read = await store.read(OUTBOX);
  read.value['r-2'].text = 'tampered';
  assert.equal(store.valueAt(`${OUTBOX}/r-2`).text, 'run 42 finished');
});

test('a node named `__proto__` is an own key of the read, and pollutes nothing', async () => {
  // `__proto__` is a legal Realtime Database key -- `isDatabaseKey('__proto__')`
  // and the schema's `keyField` both accept it, which `paths.test.js` holds the
  // two rules to -- and the database delivers it as an OWN key, because a
  // listener hands back what `JSON.parse` would build. A double that built the
  // subtree with plain assignment would instead write THROUGH the key: one
  // level up it lands on `Object.prototype` (process-wide pollution, and the
  // node vanishes from the read), and as a leaf it sets the result's PROTOTYPE
  // (no own keys at all, so `Object.entries` never sees the message and the
  // client would never speak it).
  const store = new MemoryMailboxStore();
  assert.equal(isDatabaseKey('__proto__'), true, 'the fixture key is one the rules accept');
  await store.write(`${OUTBOX}/__proto__/x`, 1);
  await store.write(`${INBOX}/__proto__`, message('__proto__'));

  const middle = await store.read(OUTBOX);
  assert.deepEqual(Object.keys(middle.value), ['__proto__'], 'the node is an own key');
  assert.equal(Object.getPrototypeOf(middle.value), Object.prototype, 'the read is an ordinary object');
  assert.deepEqual(
    Object.getOwnPropertyDescriptor(middle.value, '__proto__').value,
    { x: 1 },
    'and its value is the subtree, not a prototype',
  );
  assert.equal({}.x, undefined, 'nothing was written onto Object.prototype');

  const leaf = await store.read(INBOX);
  assert.deepEqual(Object.keys(leaf.value), ['__proto__']);
  assert.deepEqual(Object.entries(leaf.value), [['__proto__', message('__proto__')]]);
  assert.equal(Object.getPrototypeOf(leaf.value), Object.prototype);
  assert.equal({}.text, undefined, 'and the message did not become anyone\'s prototype');
});

test('a child of a node that holds a value is a container, as the database has it', async () => {
  // A Realtime Database has no mixed nodes: writing `a/b/c` when `a/b` holds a
  // scalar leaves `b` as an object with a `c`. The double must not fall over
  // (or silently drop the child) when the two writes disagree about the shape.
  const store = new MemoryMailboxStore();
  await store.write(`${OUTBOX}/r-1`, 5);
  await store.write(`${OUTBOX}/r-1/x`, 1);
  assert.deepEqual(await store.read(OUTBOX), { ok: true, value: { 'r-1': { x: 1 } } });
});

test('an unreachable store is a refusal, and the attempt is still recorded', async () => {
  // The offline phone: the write does not land, and `write-failed` is the
  // store's own code (a real adapter reports its own). What the client does
  // with it is `client.test.js`'s.
  const store = new MemoryMailboxStore({ failWrites: true, failReads: true });
  const written = await store.write(`${INBOX}/m-1`, message('m-1'));
  assert.equal(written.code, STORE_CODES.WRITE_FAILED);
  assert.equal(store.valueAt(`${INBOX}/m-1`), null);
  assert.equal(store.writes().length, 1, 'the attempt is recorded');
  assert.equal((await store.read(OUTBOX)).code, STORE_CODES.READ_FAILED);
  // ... and the same store, once the phone is back, works.
  store.setFailures({ failWrites: false, failReads: false });
  assert.deepEqual(await store.write(`${INBOX}/m-1`, message('m-1')), { ok: true });
  assert.deepEqual(await store.read(OUTBOX), { ok: true, value: null });
});

test('a path that is not a path is refused rather than stored under nothing', async () => {
  const store = new MemoryMailboxStore();
  for (const path of ['', undefined, null, 42, {}]) {
    assert.equal((await store.write(path, true)).ok, false, String(path));
    assert.equal((await store.read(path)).ok, false, String(path));
  }
  assert.deepEqual(store.reads(), [], 'nothing was read');
});

// ---------------------------------------------------------------------------
// The SDK's offline queue (P6.10, D167; G21)
// ---------------------------------------------------------------------------
//
// The second lie a fake must not tell. `setFailures({failWrites})` models a
// store that REFUSES; `setOffline(true)` models the Firebase SDK, which does not
// refuse at all -- it resolves the write locally and sends it whenever the
// network comes back. That difference is the whole of G21: a caller that is told
// nothing cannot decide anything. `client.test.js` is where the client's answer
// to it lives ("issue nothing"); these tests are what makes that answer mean
// something, by showing what the queue would otherwise have done.

test('a write made with no connection is held, not refused, and not stored', async () => {
  const store = new MemoryMailboxStore();
  store.setOffline(true);
  const written = await store.write(`${INBOX}/m-1`, message('m-1'));
  // The SDK resolves it: the caller is told the write happened.
  assert.deepEqual(written, { ok: true });
  assert.equal(store.valueAt(`${INBOX}/m-1`), null, 'nothing has been sent');
  assert.deepEqual(store.pendingWrites(), [{ path: `${INBOX}/m-1`, value: message('m-1') }]);
  // A read answers the cache -- nothing is there -- which is why an offline poll
  // would have seen an empty outbox and a heartbeat from the last time.
  assert.deepEqual(await store.read(INBOX), { ok: true, value: null });
});

test('the queue drains through the same judgement a direct write gets', async () => {
  const store = new MemoryMailboxStore();
  store.setOffline(true);
  // A message, and an ack whose parent is not there: the template's
  // `data.parent().exists()` must refuse the second when the queue reaches the
  // database, exactly as it would have refused it at once.
  await store.write(`${INBOX}/m-1`, message('m-1'));
  await store.write(`${OUTBOX}/r-1/acked`, true);
  store.setOffline(false);
  const drained = store.flushQueue();
  assert.deepEqual(drained.applied, [`${INBOX}/m-1`]);
  assert.deepEqual(drained.refused, [{ path: `${OUTBOX}/r-1/acked`, code: STORE_CODES.NO_PARENT }]);
  assert.deepEqual(store.valueAt(`${INBOX}/m-1`), message('m-1'));
  // What was refused is still held: the SDK does not silently discard it.
  assert.deepEqual(store.pendingWrites().map((entry) => entry.path), [`${OUTBOX}/r-1/acked`]);
  // ... and a re-send held while offline meets the template's `!data.exists()`
  // when the queue drains, which is exactly the duplicate P1.1's ledger answers.
  store.setOffline(true);
  await store.write(`${INBOX}/m-1`, message('m-1'));
  store.setOffline(false);
  assert.deepEqual(store.flushQueue().refused, [
    { path: `${OUTBOX}/r-1/acked`, code: STORE_CODES.NO_PARENT },
    { path: `${INBOX}/m-1`, code: STORE_CODES.EXISTS },
  ]);
});

test('the queue can be cancelled, which is the drop the client does not rely on', async () => {
  const store = new MemoryMailboxStore();
  store.setOffline(true);
  await store.write(`${INBOX}/m-1`, message('m-1'));
  await store.write(`${INBOX}/m-2`, message('m-2'));
  assert.equal(store.cancelPending(), 2);
  assert.deepEqual(store.pendingWrites(), []);
  assert.deepEqual(store.flushQueue(), { applied: [], refused: [] });
  assert.equal(store.valueAt(`${INBOX}/m-1`), null);
  assert.equal(store.cancelPending(), 0);
});

test('a queued write is not a failed one, and the two switches are not the same', async () => {
  // The distinction the whole task rests on, asserted next to each other: a
  // refused write tells the caller, a held one tells nobody.
  const store = new MemoryMailboxStore({ failWrites: true });
  const refused = await store.write(`${INBOX}/m-1`, message('m-1'));
  assert.equal(refused.ok, false);
  assert.deepEqual(store.pendingWrites(), [], 'a failure is not a queue');
  store.setFailures({ failWrites: false });
  store.setOffline(true);
  assert.deepEqual(await store.write(`${INBOX}/m-1`, message('m-1')), { ok: true });
  assert.equal(store.pendingWrites().length, 1, 'a queue is not a failure');
  // The write log carries both, and is what a test asserts "nothing was issued"
  // against: an attempt is recorded whether it landed, was refused or was held.
  assert.deepEqual(store.writes().map((entry) => entry.path), [`${INBOX}/m-1`, `${INBOX}/m-1`]);
});
