// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// One conversation, two views (mobile identity and pairing plan, Task X1;
// D2788).
//
// The task's own red step is this file's first test: **an utterance sent by
// voice must appear in the text view first**. It is written against the whole
// app's path with the device replaced -- P3.4's pipeline over its fakes, P3.5's
// speaker adapter, P3.6's mailbox client over the memory store, X1's transcript
// -- and both views:
//
//   * the voice view is `createVoiceScreen`, which accepts the owner's words and
//     records them;
//   * the text view is `createTextScreen`, drawn over a fake React with the
//     transcript's own list, so what the assertion reads is the tree a phone
//     would render rather than a list the test trusts.
//
// The four sentences this file exists for:
//
//   1. **what was said by voice is what the text view shows** -- the words, the
//      speaker, and in the order they were said;
//   2. **what was typed joins the same conversation**, through the client's own
//      `send` (the app's one write path, D113): the recording mailbox watches
//      the write, it does not make one, and the store still shows exactly one
//      inbox message per utterance;
//   3. **the agent's reply is a line too**, and it comes through the outbox: the
//      message the client read out of it is the line, the same sentence the
//      speaker said, and nothing else is recorded;
//   4. **nothing that did not reach the session is a line** -- words the gate
//      refused are absent from the conversation, which is the half that keeps
//      "the transcript is what was said" from quietly becoming "the transcript
//      is what was attempted".
//
// React and react-native are not installed (the Expo toolchain is not in this
// environment), so the four primitives are fakes and the text view is walked as
// a tree, exactly as `screen.test.js` walks it. The mailbox fixtures are
// synthetic: Migration D111's item, D171's key, and no value recorded from any
// installation.

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
import { GATE_CODES, OFFLINE_SPEECH } from '../mailbox/gate.js';
import { createPipelineSpeaker } from '../voice/speaker.js';
import {
  NO_CONVERSATION,
  SCREEN,
  createTextScreen,
  createTranscript,
  createTypedInput,
  recordingMailbox,
  transcriptSpeaker,
} from './index.js';
import { createVoiceScreen } from '../voice/index.js';

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

const INBOX = `mailbox/${UID}/inbox`;
const OUTBOX = `mailbox/${UID}/outbox`;
const HEARTBEAT = `mailbox/${UID}/heartbeat`;

/** Let the app's promise chains drain, exactly as `voice/screen.test.js` does. */
const settle = () => new Promise((resolve) => setImmediate(resolve));

// ---------------------------------------------------------------------------
// The text view, as a tree a phone would render.
// ---------------------------------------------------------------------------

/** The smallest React that can build a tree we can read. */
const fakeReact = {
  createElement(type, props, ...children) {
    return { type, props: props ?? null, children };
  },
};

const View = function View() {};
const Text = function Text() {};
const TextInput = function TextInput() {};

const TextScreen = createTextScreen({ React: fakeReact, View, Text, TextInput });

/** Every element in the tree, depth first. */
function walk(node, found = []) {
  if (node === null || typeof node !== 'object') return found;
  found.push(node);
  for (const child of node.children ?? []) walk(child, found);
  return found;
}

/**
 * The conversation as the text view draws it: one `[who, words]` pair per line,
 * in the order the tree holds them. This is the assertion's subject -- the words
 * a screenshot would show, not the list the test passed in.
 */
function drawn(conversation) {
  return walk(TextScreen({ conversation }))
    .filter((one) => one.props !== null && one.props.type === SCREEN.LINE)
    .map((line) =>
      line.children.flatMap((child) => child.children.filter((child) => typeof child === 'string')),
    );
}

/** Every string the text view would show -- for the "nothing was said" case. */
function shown(conversation) {
  return walk(TextScreen({ conversation }))
    .filter((one) => one.type === Text)
    .flatMap((one) => one.children.filter((child) => typeof child === 'string'));
}

// ---------------------------------------------------------------------------
// The app's path, with the device replaced.
// ---------------------------------------------------------------------------

/**
 * One phone: P3.4's pipeline over its fakes, P3.6's client over the memory
 * store with X1's speaker behind it, X1's transcript, and both views over it.
 * `now` is a variable rather than a clock -- nothing here reads the time.
 */
async function harness() {
  const audioIn = new FakeAudioIn();
  const stt = new FakeStt();
  const tts = new FakeTts();
  let nowMs = T0;
  let messageIds = 0;
  let clientIds = 0;
  let typedIds = 0;
  let lineIds = 0;
  const pipeline = createVoicePipeline({
    audioIn,
    stt,
    tts,
    sender: UID,
    newId: () => `p-${++messageIds}`,
    now: () => nowMs,
  });
  const keys = new SecretStore(new MemorySecureStore());
  const made = secret('macKey', MAC_KEY);
  assert.equal(made.ok, true, 'the fixture key is a valid secret');
  assert.equal((await keys.put(ACCOUNT.id, 'macKey', made.secret)).ok, true, 'the fixture key is stored');

  const db = new MemoryMailboxStore();
  const transcript = createTranscript({ now: () => nowMs, newId: () => `l-${++lineIds}` });
  const client = createMailboxClient({
    account: ACCOUNT,
    keys,
    store: db,
    connection: new MemoryConnection(),
    // The reply reaches the conversation through the client's own speaker port,
    // which is D2788's "replies arrive through the mailbox outbox": one read,
    // and no second door for the agent's words.
    speaker: transcriptSpeaker({ transcript, speaker: createPipelineSpeaker({ pipeline }) }),
    now: () => nowMs,
    newId: () => `m-${++clientIds}`,
    secrets: SECRETS,
    heartbeatIntervalMs: INTERVAL_MS,
  });
  const screen = createVoiceScreen({
    pipeline,
    client,
    now: () => nowMs,
    holder: 'phone-installation-a',
    transcript,
  });
  const typed = createTypedInput({
    // The typed door is `input.js`'s and unchanged: it is handed the client with
    // one member watched, so the words it sends are the words that reach the
    // conversation.
    mailbox: recordingMailbox({ transcript, mailbox: client }),
    now: () => nowMs,
    newId: () => `u-${++typedIds}`,
  });

  return {
    audioIn,
    stt,
    tts,
    pipeline,
    client,
    screen,
    typed,
    transcript,
    db,
    /** Every path the store was written to, in order. */
    pathsWritten: () => db.writes().map((entry) => entry.path),
    /** Move the clock, so P1.4's presence can age without a wait. */
    advance: (ms) => {
      nowMs += ms;
    },
    /** Plant the orchestrator's signed heartbeat, the way `fugaro watch` does. */
    plantHeartbeat: async () => {
      const base = { at: nowMs - 1_000, boot: 'boot-0001' };
      await db.write(HEARTBEAT, { ...base, mac: macOf(base, MAC_KEY, 'heartbeat') });
    },
    /** Plant a signed reply in the phone's outbox, as the consumer writes it. */
    plantReply: async (id, text) => {
      const base = { id, sender: UID, text, at: nowMs - 1_000 };
      await db.write(`${OUTBOX}/${id}`, { ...base, mac: macOf(base, MAC_KEY, 'outbox') });
    },
    /** The phone looks, then the owner speaks one utterance into the pipeline. */
    speak: async (text) => {
      await screen.start();
      audioIn.speechStart();
      audioIn.speechEnd();
      stt.final(text);
      await settle();
      // A refusal is SPOKEN (the gate's own sentence, through the app's own
      // speaker), so the utterance is played out the way every other one in the
      // voice tests is: the fake settles when the test says so, and a
      // successful send has nothing in flight and refuses nothing.
      tts.finish();
      await settle();
    },
  };
}

// ---------------------------------------------------------------------------
// 1. What was said by voice is what the text view shows.
// ---------------------------------------------------------------------------

test('the red step: an utterance sent by voice appears in the text view', async () => {
  const h = await harness();
  assert.deepEqual(drawn(h.transcript.entries()), [], 'nothing has been said yet');

  // P6.10's gate fails closed on a consumer nothing has observed, so the phone
  // looks first -- otherwise the words are refused as unobserved rather than
  // written, and the assertion below would be about the gate.
  await h.plantHeartbeat();
  await h.screen.poll();
  await h.speak('run the tests');

  assert.deepEqual(h.pathsWritten().filter((path) => path.startsWith(`${INBOX}/`)), [`${INBOX}/m-1`],
    'the words reached the session, through the app\'s one write path');
  assert.deepEqual(
    h.transcript.entries().map((one) => [one.who, one.source, one.text, one.engine]),
    [['operator', 'voice', 'run the tests', 'local']],
    'and the voice view recorded them, with the engine that produced them',
  );
  // The text view is a different screen drawn from the same list, and the
  // assertion is against the tree it builds.
  assert.deepEqual(drawn(h.transcript.entries()), [['You', 'run the tests']]);
  // ... and the voice view shows the same conversation, so the switch is one
  // list read twice rather than two lists that agree today.
  assert.deepEqual(h.screen.state().conversation, h.transcript.entries());
});

// ---------------------------------------------------------------------------
// 2. A typed line joins the same conversation.
// ---------------------------------------------------------------------------

test('a typed line is the same conversation, and the client is still the one that sent it', async () => {
  const h = await harness();
  await h.plantHeartbeat();
  await h.screen.poll();
  await h.speak('run the tests');

  const sent = await h.typed.submit('ship the release notes');

  assert.equal(sent.ok, true, 'the typed door sent it');
  assert.deepEqual(h.pathsWritten().filter((path) => path.startsWith(`${INBOX}/`)),
    [`${INBOX}/m-1`, `${INBOX}/m-2`],
    'one inbox message per utterance, written by the client and by nothing else');
  assert.deepEqual(
    h.transcript.entries().map((one) => [one.who, one.source, one.text, one.id]),
    [
      ['operator', 'voice', 'run the tests', 'l-1'],
      ['operator', 'text', 'ship the release notes', 'l-2'],
    ],
  );
  // The order is the conversation's own, and the text view shows it: what was
  // said by voice is above what was typed, and the switch loses neither.
  assert.deepEqual(drawn(h.transcript.entries()), [
    ['You', 'run the tests'],
    ['You', 'ship the release notes'],
  ]);
  assert.deepEqual(h.screen.state().conversation.map((one) => one.text),
    ['run the tests', 'ship the release notes'],
    'the voice view, after the owner typed while the text view was up');
});

// ---------------------------------------------------------------------------
// 3. The agent's reply arrives through the outbox.
// ---------------------------------------------------------------------------

test('a reply read out of the outbox is the agent\'s line, and the third line of the conversation', async () => {
  const h = await harness();
  await h.plantHeartbeat();
  await h.screen.poll();
  await h.speak('run the tests');
  await h.plantReply('r-1', 'Run 42 finished.');

  // The reply is SPOKEN by the client, and the fake settles when the test says
  // so: the pass is not finished until the utterance is.
  const polling = h.screen.poll();
  await settle();
  assert.deepEqual(h.tts.spoken(), ['Run 42 finished.'], 'the reply reached the audio pipeline');
  h.tts.finish();
  await polling;

  assert.deepEqual(h.transcript.entries().map((one) => [one.who, one.source, one.text]),
    [
      ['operator', 'voice', 'run the tests'],
      ['agent', 'outbox', 'Run 42 finished.'],
    ]);
  // The reply is the sentence the speaker said -- not a second read of the
  // record, and not a line invented for a message that was only acked.
  // (`writes()` counts the fixture's own outbox record above, so the claim is
  // about the acks the client made.)
  assert.deepEqual(h.pathsWritten().filter((path) => path.endsWith('/acked')), [`${OUTBOX}/r-1/acked`],
    'and the message is acked, because it was spoken (P3.6\'s rule)');
  assert.deepEqual(drawn(h.transcript.entries()), [
    ['You', 'run the tests'],
    ['Agent', 'Run 42 finished.'],
  ]);
});

// ---------------------------------------------------------------------------
// 4. What did not reach the session is not a line.
// ---------------------------------------------------------------------------

test('words the gate refused are absent from the conversation, and the view says nothing was said', async () => {
  const h = await harness();
  await h.screen.poll();

  await h.speak('run the tests');

  assert.deepEqual(h.pathsWritten(), [], 'nothing reached the store');
  assert.equal(h.screen.state().refused.code, 'offline', 'and the voice view shows why');
  assert.equal(h.screen.state().transcript.text, 'run the tests', 'what was heard is still shown');
  assert.deepEqual(h.transcript.entries(), [], 'but the conversation did not take it');
  assert.equal(
    shown(h.transcript.entries()).includes(NO_CONVERSATION),
    true,
    'and the view says nothing has been said rather than drawing a line nobody heard',
  );
  assert.deepEqual(drawn(h.transcript.entries()), []);
});

// ---------------------------------------------------------------------------
// 5. The app's own sentence is not the agent talking.
// ---------------------------------------------------------------------------

test('the refusal the app speaks over the same port is not an agent line in the text view', async () => {
  const h = await harness();
  await h.plantHeartbeat();
  await h.screen.poll();
  // The orchestrator's heartbeat goes quiet: P1.4's `stale`, which the screen
  // does not call offline, so the words reach the client and the CLIENT refuses
  // them -- speaking the gate's sentence through the very port the transcript
  // sits behind (S6.3). The same port speaks the outbox's replies, so this is
  // the case the wrapper must tell apart.
  h.advance(5 * INTERVAL_MS);

  await h.speak('run the tests');

  assert.deepEqual(h.tts.spoken(), [OFFLINE_SPEECH[GATE_CODES.CONSUMER_STALE]], 'the owner was told why');
  assert.deepEqual(h.pathsWritten().filter((path) => path.startsWith(`${INBOX}/`)), [], 'and nothing was sent');
  assert.deepEqual(h.transcript.entries(), [],
    'and the app telling the owner about itself is not the agent speaking');
  assert.equal(shown(h.transcript.entries()).includes(NO_CONVERSATION), true,
    'so the view still says nothing has been said');
});
