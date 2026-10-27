// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The wiring P3.6 left open, held to its own rule (mobile plan, Task P3.5;
// Global Constraint 10, Voice D52).
//
// `mobile/README.md` carried the sentence this file makes true: *`speaker.js`
// stays until the voice screen (P3.5) wires the mailbox client to the audio
// pipeline*. The client's rule is that it **acks a message only after it has
// spoken it**, and until now the speaker it was handed was a stub with no
// audio behind it. Here the pipeline is behind it, over P3.4's fakes -- no
// microphone, no synthesizer, no device -- and both halves of the client's
// rule are asserted through the real client rather than restated:
//
//   * a reply that played out is spoken AND acked;
//   * a reply that did not play out (a barge-in, or an utterance already in
//     flight) is **not** acked, and the next pass tries again.
//
// Every secret-shaped string below is synthetic and is not recorded from any
// installation (`belong` least of all, Global Constraint 11).

import assert from 'node:assert/strict';
import test from 'node:test';

import { MemorySecureStore, SecretStore, secret } from '../accounts/index.js';
import { FakeAudioIn } from '../audio/audio-in.js';
import { createVoicePipeline } from '../audio/pipeline.js';
import { FakeStt } from '../audio/stt.js';
import { FakeTts } from '../audio/tts.js';
import { createMailboxClient } from '../mailbox/client.js';
import { MemoryConnection } from '../mailbox/connection.js';
import { macOf } from '../mailbox/mac.js';
import { MemoryMailboxStore } from '../mailbox/store.js';
import { createPipelineSpeaker } from './speaker.js';

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
const INTERVAL_MS = 60_000;

/** D171's per-pairing key, synthetic: 32 bytes of hex is what the store holds. */
const MAC_KEY = 'synthetic-mac-key-0123456789abcdef';
/** D118's secret list: what must not leave the device. */
const SECRETS = ['synthetic-token-0123456789'];

const OUTBOX = `mailbox/${UID}/outbox`;

/**
 * Let the client's own promise chain drain, so a test can assert what the
 * audio pipeline was asked for *while an utterance is still in flight*.
 *
 * `setImmediate` and not a timer: it schedules no delay, it only yields until
 * the microtask queue is empty. Nothing under `src/voice/` that ships has one
 * of these -- the module's scan forbids a timer in production code -- and it is
 * here for the reason `FakeTts` exists at all: the test decides when an
 * utterance ends, so the test has to be able to look in the moment before it
 * does.
 */
const settle = () => new Promise((resolve) => setImmediate(resolve));

/** Put the pairing key in the store, the way `fugaro pair` would (D111). */
async function pair(keys) {
  const made = secret('macKey', MAC_KEY);
  assert.equal(made.ok, true, 'the fixture key is a valid secret');
  const written = await keys.put(ACCOUNT.id, 'macKey', made.secret);
  assert.equal(written.ok, true, 'the fixture key is stored');
}

/**
 * A pipeline over P3.4's three fakes, and the mailbox client speaking through
 * it -- the app's real shape, with only the device replaced.
 */
async function harness() {
  const audioIn = new FakeAudioIn();
  const stt = new FakeStt();
  const tts = new FakeTts();
  let nowMs = T0;
  let minted = 0;
  const pipeline = createVoicePipeline({
    audioIn,
    stt,
    tts,
    sender: UID,
    newId: () => `p-${++minted}`,
    now: () => nowMs,
  });
  const speaker = createPipelineSpeaker({ pipeline });
  const keys = new SecretStore(new MemorySecureStore());
  await pair(keys);
  const db = new MemoryMailboxStore();
  const client = createMailboxClient({
    account: ACCOUNT,
    keys,
    store: db,
    connection: new MemoryConnection(),
    speaker,
    now: () => nowMs,
    newId: () => `m-${++minted}`,
    secrets: SECRETS,
    heartbeatIntervalMs: INTERVAL_MS,
  });
  return {
    audioIn,
    stt,
    tts,
    pipeline,
    speaker,
    client,
    db,
    /** Plant a signed reply in the phone's outbox, the way the consumer writes one. */
    plantReply: async (id, text) => {
      const base = { id, sender: UID, text, at: nowMs - 1_000 };
      await db.write(`${OUTBOX}/${id}`, { ...base, mac: macOf(base, MAC_KEY, 'outbox') });
    },
    /** The ack node of a reply, which is the only thing the phone writes here. */
    acked: (id) => db.valueAt(`${OUTBOX}/${id}/acked`),
  };
}

test('the port refuses a pipeline that is not one, at the point the app is built', () => {
  // A wiring bug, and it throws here rather than at the first reply the owner
  // was waiting for -- the same rule `createVoicePipeline` and
  // `createMailboxClient` hold.
  assert.throws(() => createPipelineSpeaker({}), TypeError);
  assert.throws(() => createPipelineSpeaker({ pipeline: { say: () => {} } }), TypeError);
  assert.throws(() => createPipelineSpeaker(), TypeError);
});

test('a reply that played out is a spoken verdict, and one that did not is not', async () => {
  const h = await harness();
  // Played to the end.
  const said = h.speaker.speak('Two tests failed.');
  assert.equal(h.tts.speaking(), true);
  h.tts.finish();
  assert.deepEqual({ ...(await said) }, { ok: true }, 'the client asks one question: did the owner hear it?');

  // A barge-in: §5's "user speech during TTS stops playback immediately". The
  // audio layer calls it `interrupted`; to the client it is a failed speak, and
  // the answer is the one the client's rule is written against.
  const barged = h.speaker.speak('A long sentence the user talks over.');
  h.tts.cancel();
  assert.deepEqual({ ...(await barged) }, { ok: false, code: 'speak-failed', field: 'text' });
  assert.deepEqual(h.tts.interrupted(), ['A long sentence the user talks over.'], 'and it really was interrupted');

  // Nothing to say, which is the one refusal the caller can fix and so the one
  // carried through as itself.
  assert.deepEqual({ ...(await h.speaker.speak('')) }, { ok: false, code: 'bad-text', field: 'text' });
  // ... and a second utterance while one is playing is a failed speak too: the
  // pipeline's own `already-speaking`, which the client never sees.
  const flight = h.speaker.speak('the first one');
  assert.deepEqual({ ...(await h.speaker.speak('the second one')) }, { ok: false, code: 'speak-failed', field: 'text' });
  h.tts.finish();
  assert.deepEqual({ ...(await flight) }, { ok: true });
});

test('the plan\'s wiring: the client speaks a reply through the audio pipeline, and acks it', async () => {
  const h = await harness();
  await h.plantReply('r-1', 'run 42 finished');

  const polling = h.client.poll();
  await settle();
  assert.deepEqual(h.tts.spoken(), ['run 42 finished'], 'the reply reached the audio pipeline');
  // The utterance is in flight and the pass has not finished: the ack has not
  // been written, because the speaking has not finished. That is the order the
  // client's rule is about, asserted at the moment before it is decided.
  assert.equal(h.acked('r-1'), null, 'no ack while the utterance is still playing');

  h.tts.finish();
  const report = await polling;

  assert.deepEqual(report.spoken, ['r-1']);
  assert.equal(h.acked('r-1'), true, 'and it was acked, because it was spoken');
  assert.deepEqual(report.unspoken, []);
});

test('a reply the pipeline could not speak is not acked, and the next pass tries again', async () => {
  const h = await harness();
  await h.plantReply('r-1', 'run 42 finished');

  // An utterance is already in flight -- the assistant is mid-sentence -- so
  // the pipeline refuses a second one and the client must not ack. This is the
  // case the stub could only be told to fake; here it is the real port's own
  // `already-speaking`, raised by the real pipeline.
  const inFlight = h.pipeline.say('something the assistant is already saying');
  const blocked = await h.client.poll();
  assert.deepEqual(blocked.spoken, []);
  assert.deepEqual(blocked.unspoken, [{ id: 'r-1', code: 'speak-failed' }]);
  assert.equal(h.acked('r-1'), null, 'nothing was acked for a reply nobody heard');
  h.tts.finish();
  await inFlight;

  // ... and once the assistant has finished, the same pass speaks it and acks
  // it: an unacked message is a message that is tried again, not one that is
  // lost.
  const second = h.client.poll();
  await settle();
  assert.deepEqual(
    h.tts.spoken(),
    ['something the assistant is already saying', 'run 42 finished'],
    'the same reply, tried again -- and only after the assistant finished',
  );
  h.tts.finish();
  const report = await second;
  assert.deepEqual(report.spoken, ['r-1']);
  assert.equal(h.acked('r-1'), true);
});
