// The demo's transport, held to the two things it claims (owner ruling of
// 2026-10-19).
//
// `transport.js` claims that what it plants is (a) the app's real client's
// substrate -- the double's own read semantics, and the schema's own validation
// -- and (b) tagged by the app's own MAC code, so the client's very real
// verification is what decides whether a reviewer hears a reply. Both claims
// are load-bearing: if the tags were made by anything else, or the sample data
// bypassed validation, the demo would be a transcript and not a run. So both
// are asserted here, next to the code that makes them, including the negative
// case that a WRONGLY tagged message is dropped rather than spoken.

import assert from 'node:assert/strict';
import test from 'node:test';

import { createMailboxClient } from '../mailbox/index.js';
import { macOf } from '../mailbox/mac.js';
import { demoKeySource, DEMO_KEY_ITEM, DEMO_PAIRING_KEY } from './keys.js';
import { DEMO_ACCOUNT, DEMO_INTERVAL_MS, DEMO_NOW, DEMO_PROMPT, DEMO_REPLIES } from './sample.js';
import { createDemoTransport, demoIds } from './transport.js';

test('the sample data is valid under the schema the client reads it with', () => {
  // `createDemoTransport` validates and throws on a bad sample; this asserts the
  // validation is reached at all, and that the sample is what the demo's own
  // screen then shows.
  const transport = createDemoTransport();
  assert.equal(transport.messages.length, DEMO_REPLIES.length + 1);
  assert.ok(transport.messages.includes(transport.prompt));
  assert.deepEqual(transport.messages.map((message) => message.id), ['demo-reply-1', 'demo-reply-2', 'demo-prompt-1']);
  assert.equal(transport.heartbeat.at, DEMO_NOW);
  assert.deepEqual(transport.heartbeat.agents, [{ id: 'demo-agent', name: 'Demo agent' }]);
});

test('every planted message carries the tag the app\'s own code makes', () => {
  // The tag is over the record WITHOUT its `mac` (what `canonicalBytes` omits),
  // and it is made by `mac.js` -- the same implementation the client verifies
  // with. Recomputing it here with the same call is what makes the client's
  // verification in `index.test.js` a statement about the app rather than about
  // the demo agreeing with itself: change the direction, or drop a field from
  // the record, and this fails.
  const transport = createDemoTransport();
  for (const message of transport.messages) {
    const { mac, ...record } = message;
    assert.equal(mac, macOf(record, DEMO_PAIRING_KEY, 'outbox'), message.id);
  }
  const { mac, ...beat } = transport.heartbeat;
  assert.equal(mac, macOf(beat, DEMO_PAIRING_KEY, 'heartbeat'));

  // The placeholder key is a placeholder: a reviewer who finds it in a
  // screenshot is meant to read it as one.
  assert.ok(DEMO_PAIRING_KEY.startsWith('demo'), DEMO_PAIRING_KEY);
  assert.equal(DEMO_KEY_ITEM, 'macKey');
});

test('the transport plants the other end\'s side, and no phone write', () => {
  // Nothing the client will do is pre-answered: the store holds the consumer's
  // messages and the heartbeat and NOT the phone's acks, and the write log --
  // the record of what the CLIENT asked for -- is empty until the client runs.
  const transport = createDemoTransport();
  assert.deepEqual(transport.store.writes(), []);
  assert.equal(transport.store.valueAt(transport.paths.outboxMessage('demo-reply-1')).mac.length, 64);
  assert.equal(transport.store.valueAt(transport.paths.heartbeat).boot, 'demo-boot-1');
  assert.equal(transport.store.valueAt(transport.paths.inbox), null);
});

test('the demo\'s id source names the utterances the sample replies answer', () => {
  // This is the seam that makes the consumer's answers answers: the counter
  // mints `demo-utterance-1`, `demo-utterance-2`, ... and `DEMO_REPLIES` names
  // exactly those ids. A demo id source that minted anything else would show a
  // reviewer two replies to nothing -- and the client would re-send the
  // utterances, because an unanswered utterance stays in its outbox.
  const ids = demoIds();
  assert.deepEqual(DEMO_REPLIES.map((reply) => reply.replyTo), [ids(), ids()]);
  for (const reply of DEMO_REPLIES) assert.ok(reply.replyTo.startsWith('demo-utterance-'));
  // ... and the ids are fresh per call, so the confirmation is never the id of
  // the command it confirms.
  assert.notEqual(ids(), ids());
});

test('a reply tagged with the wrong key is DROPPED by the app\'s own verifier', async () => {
  // The negative case, and the reason the demo's client is the real client: if
  // the demo spoke whatever was in the store, this test would hear the reply
  // spoken. It is the app's verification -- `mac.js`'s, with the key the demo's
  // source hands out -- and the dropped message is not spoken and not acked.
  const transport = createDemoTransport();
  const forged = { ...transport.messages[0] };
  const { mac, ...record } = forged;
  transport.store.deliver(transport.paths.outboxMessage(forged.id), {
    ...record,
    text: 'Demo: the reviewer is owed a refund.',
    mac: macOf({ ...record, text: 'Demo: something else entirely.' }, DEMO_PAIRING_KEY, 'outbox'),
  });

  const client = createMailboxClient({
    account: DEMO_ACCOUNT,
    keys: demoKeySource(),
    store: transport.store,
    speaker: transport.speaker,
    now: () => DEMO_NOW,
    newId: demoIds(),
    secrets: [],
    heartbeatIntervalMs: DEMO_INTERVAL_MS,
  });
  const poll = await client.poll();
  assert.deepEqual(poll.dropped.map((entry) => entry.id), [forged.id]);
  assert.ok(!transport.speaker.spoken().includes('Demo: the reviewer is owed a refund.'));
  assert.equal(transport.store.valueAt(transport.paths.acked(forged.id)), null);
});

test('the transport refuses a key that is not one', () => {
  for (const key of ['', null, 42]) {
    assert.throws(() => createDemoTransport({ key }), TypeError, String(key));
  }
  // No key at all is the placeholder, not an error: the demo has exactly one.
  assert.equal(createDemoTransport({}).messages[0].mac, createDemoTransport().messages[0].mac);
  assert.equal(createDemoTransport({ key: DEMO_PAIRING_KEY }).messages.length, 3);
});

test('the question is a prompt in the outbox, and its options are the ones offered', () => {
  const transport = createDemoTransport();
  const planted = transport.store.valueAt(transport.paths.outboxMessage(DEMO_PROMPT.id));
  assert.equal(planted.kind, 'prompt');
  assert.deepEqual(planted.options, [...DEMO_PROMPT.options]);
  assert.equal(planted.text, DEMO_PROMPT.text);
});
