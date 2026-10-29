// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The voice screen's rules (mobile plan, Task P3.5; D122, D127, D126, D118).
//
// The plan's verification is four sentences, and this file is those four, in
// order, over the fakes -- no device, no network, no database, no microphone
// (Global Constraint 10):
//
//   1. *with the interrupt switch off, a proactive update is queued and not
//      spoken over the user; with it on, it is spoken at the next boundary and
//      **never** over an in-progress utterance;*
//   2. *the offline state is shown from P1.4 and the app **refuses to accept a
//      message** while offline;*
//   3. *a lease held by another consumer is surfaced rather than ignored;*
//   4. *and the digest's highest severity is the only thing that interrupts
//      when the switch is on.*
//
// The pipeline, the mailbox client and the three audio ports are the real ones
// (P3.4 and P3.6, merged), each built the way the app builds it, with only the
// device replaced: the database is `MemoryMailboxStore`, the speaker is P3.4's
// pipeline, and the microphone, the engine and the voice are P3.4's fakes. That
// is what makes "the app refuses to accept a message" a claim about **the
// store's write log** rather than about a returned value.
//
// The offline case is P1.4's own rule read through the real client: a mailbox
// with no heartbeat is `offline` with `no-heartbeat`, and one heartbeat younger
// than an interval is `live`. Both are planted here, so the screen is held to
// P1.4 rather than to a state a test typed in.
//
// Every secret-shaped string below is synthetic and is not recorded from any
// installation (`belong` least of all, Global Constraint 11).

import assert from 'node:assert/strict';
import test from 'node:test';

import { SEVERITY } from '../../../shared/digest/index.js';
import { emptyLeaseState, takeLease } from '../../../shared/lease/index.js';
import { STATE } from '../../../shared/presence/index.js';
import { MemorySecureStore, SecretStore, secret } from '../accounts/index.js';
import { AUDIO_IN_CODES, FakeAudioIn } from '../audio/audio-in.js';
import { createVoicePipeline } from '../audio/pipeline.js';
import { FakeStt } from '../audio/stt.js';
import { FakeTts } from '../audio/tts.js';
import { createMailboxClient } from '../mailbox/client.js';
import { MemoryConnection } from '../mailbox/connection.js';
import { GATE_CODES } from '../mailbox/gate.js';
import { macOf } from '../mailbox/mac.js';
import { MemoryMailboxStore } from '../mailbox/store.js';
import { QUEUE_CAP, createVoiceScreen } from './screen.js';
import { INTERRUPT_SWITCH } from './interrupt.js';
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

const HEARTBEAT = `mailbox/${UID}/heartbeat`;

/** The phone's own holder name, which is what P1.2 records on a lease. */
const PHONE = 'phone-installation-a';
/** The other consumer -- the laptop's `fugaro watch` -- and never this phone. */
const LAPTOP = 'laptop-watch';

/**
 * Let the app's promise chains drain before a test looks at the store, so that
 * "nothing was written" is asserted after the write would have happened.
 *
 * `setImmediate` and not a timer: it schedules no delay, it only yields until
 * the microtask queue is empty. No file under `src/voice/` that ships has one
 * of these -- the module's scan forbids a timer in production code.
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
 * The whole app's voice path, with the device replaced: P3.4's pipeline over
 * its three fakes, P3.5's speaker adapter, P3.6's client over the memory store,
 * and the screen over both. `now` is a variable rather than a clock -- nothing
 * here reads the time, and a test that wants "an hour later" says so.
 */
async function harness(options = {}) {
  const audioIn = new FakeAudioIn(options.refuse ? { refuse: options.refuse } : {});
  const stt = new FakeStt();
  const tts = new FakeTts();
  let nowMs = T0;
  // Two id sources, so a test can tell the pipeline's message ids from the
  // client's: they are different things, and a shared counter would make the
  // store's paths read as `m-2` for the first message the owner sends.
  let messageIds = 0;
  let clientIds = 0;
  const pipeline = createVoicePipeline({
    audioIn,
    stt,
    tts,
    sender: UID,
    newId: () => `p-${++messageIds}`,
    now: () => nowMs,
  });
  const keys = new SecretStore(new MemorySecureStore());
  await pair(keys);
  const db = new MemoryMailboxStore();
  const client = createMailboxClient({
    account: ACCOUNT,
    keys,
    store: db,
    connection: new MemoryConnection(),
    speaker: createPipelineSpeaker({ pipeline }),
    now: () => nowMs,
    newId: () => `m-${++clientIds}`,
    secrets: SECRETS,
    heartbeatIntervalMs: INTERVAL_MS,
  });
  const screen = createVoiceScreen({
    pipeline,
    client,
    now: () => nowMs,
    holder: PHONE,
    ...(options.switch === undefined ? {} : { switch: options.switch }),
    ...(options.queueCap === undefined ? {} : { queueCap: options.queueCap }),
    ...(options.utterance === undefined ? {} : { utterance: options.utterance }),
  });
  return {
    audioIn,
    stt,
    tts,
    pipeline,
    client,
    screen,
    db,
    setNow: (value) => {
      nowMs = value;
    },
    advance: (ms) => {
      nowMs += ms;
    },
    now: () => nowMs,
    /** How many times the store was written to, so a test can assert a delta. */
    writes: () => db.writes().length,
    /** Every path the store was written to, in order. */
    pathsWritten: () => db.writes().map((entry) => entry.path),
    /** Plant a signed heartbeat at the mailbox's node, the orchestrator's write. */
    plantHeartbeat: async (fields = {}) => {
      const base = { at: nowMs - 1_000, boot: 'boot-0001', ...fields };
      await db.write(HEARTBEAT, { ...base, mac: macOf(base, MAC_KEY, 'heartbeat') });
    },
  };
}

/** A digest carrying one severity, which is the field this screen reads. */
const digestOf = (severity) => ({ severity });

// --- the plan, sentence one ------------------------------------------------

test('the plan, sentence one: with the switch off a proactive update is queued, and not spoken', async () => {
  const h = await harness();
  h.screen.setDigest(digestOf(SEVERITY.kill));

  const offered = h.screen.offer({ text: 'a run was killed', severity: SEVERITY.kill });

  assert.deepEqual({ ...offered }, { ok: true, queued: 1 });
  assert.deepEqual(h.tts.spoken(), [], 'the switch is off: nothing was said');
  assert.deepEqual(h.screen.state().queued, [{ text: 'a run was killed', severity: 'kill' }]);
  assert.deepEqual(h.screen.queued(), [{ text: 'a run was killed', severity: 'kill' }], 'and the reader answers with the same list');
  assert.equal(Object.isFrozen(h.screen.queued()), true, 'a list handed out is a list frozen');
  assert.equal(h.screen.state().switch, INTERRUPT_SWITCH.OFF, 'and off is where a phone starts');
  // The boundary is free -- nobody is speaking and no turn is open -- and it is
  // still not spoken, which is what makes the switch the reason rather than the
  // moment.
  assert.equal(h.pipeline.speaking(), false);
  assert.equal(h.pipeline.turn(), false);

  // ... and turning it on is the boundary: the queue drains at once, because
  // nothing else is happening.
  assert.deepEqual({ ...h.screen.setSwitch(INTERRUPT_SWITCH.ON) }, { ok: true, switch: 'on' });
  assert.deepEqual(h.tts.spoken(), ['a run was killed']);
  assert.deepEqual(h.screen.state().queued, [], 'and the queue is empty, because it was spoken');
});

test('with the switch on, an update waits for the boundary and is never spoken over an utterance', async () => {
  const h = await harness({ switch: INTERRUPT_SWITCH.ON });
  h.screen.setDigest(digestOf(SEVERITY.failed));

  // The assistant is mid-sentence -- a reply the mailbox client is reading out.
  const reply = h.pipeline.say('Run 42 finished.');
  assert.equal(h.pipeline.speaking(), true);

  h.screen.offer({ text: 'a run failed', severity: SEVERITY.failed });
  assert.deepEqual(h.tts.spoken(), ['Run 42 finished.'], 'nothing was spoken over the in-progress utterance');
  assert.deepEqual(h.screen.state().queued, [{ text: 'a run failed', severity: 'failed' }], 'it is queued instead');

  // The utterance ends: that is the boundary, and the update is spoken then.
  h.tts.finish();
  await reply;
  assert.deepEqual(h.tts.spoken(), ['Run 42 finished.', 'a run failed']);
  assert.deepEqual(h.screen.state().queued, []);
});

test('with the switch on, an update waits for the user\'s turn to close, and is spoken after it', async () => {
  const h = await harness({ switch: INTERRUPT_SWITCH.ON });
  h.screen.setDigest(digestOf(SEVERITY.failed));
  // **The phone has looked.** P6.10's gate fails closed on a consumer nothing
  // has observed (the review's M2), so the app's first act is the poll that
  // says whether the orchestrator is there at all -- the same pass `demo`
  // makes, and the state a running phone is in. Without it the user's own
  // words would be refused as unobserved rather than written, which is this
  // test's subject.
  await h.plantHeartbeat();
  await h.screen.poll();

  // The user is being listened to. Nothing may interrupt them -- the queue is
  // what "rather than accepting messages into the void" means on this side.
  await h.screen.start();
  h.audioIn.speechStart();
  assert.equal(h.pipeline.turn(), true);
  h.screen.offer({ text: 'a run failed', severity: SEVERITY.failed });
  assert.deepEqual(h.tts.spoken(), [], 'nothing is spoken while the user is speaking');
  assert.equal(h.screen.state().queued.length, 1);

  // The turn ends with the engine's final: their words become a message through
  // the client, and then the boundary is free and the update is spoken.
  h.audioIn.speechEnd();
  h.stt.final('run the tests');
  await settle();
  assert.deepEqual(h.tts.spoken(), ['a run failed']);
  assert.deepEqual(
    h.pathsWritten().filter((path) => path.includes('/inbox/')),
    [`mailbox/${UID}/inbox/m-1`],
    'and the user\'s own words were written by the client, as a message',
  );
});

test('one utterance at a time: two queued updates are spoken one after the other, not together', async () => {
  const h = await harness({ switch: INTERRUPT_SWITCH.ON });
  h.screen.setDigest(digestOf(SEVERITY.info));

  h.screen.offer({ text: 'the first', severity: SEVERITY.info });
  assert.deepEqual(h.tts.spoken(), ['the first']);
  h.screen.offer({ text: 'the second', severity: SEVERITY.info });
  // The first is still playing, so the second waits -- `already-speaking` is
  // never reached, because the screen does not ask the port to queue.
  assert.deepEqual(h.tts.spoken(), ['the first']);
  assert.deepEqual(h.screen.state().queued, [{ text: 'the second', severity: 'info' }]);

  h.tts.finish();
  await settle();
  assert.deepEqual(h.tts.spoken(), ['the first', 'the second']);
  assert.deepEqual(h.screen.state().queued, []);
});

// --- the plan, sentence four -----------------------------------------------

test('the plan, sentence four: the digest\'s highest severity is the only thing that interrupts', async () => {
  const h = await harness({ switch: INTERRUPT_SWITCH.ON });
  h.screen.setDigest(digestOf(SEVERITY.failed));

  // Two updates below the digest's top: both are queued, neither is spoken.
  h.screen.offer({ text: 'the burn is fast', severity: SEVERITY.fast_burn });
  h.screen.offer({ text: 'a run is running', severity: SEVERITY.info });
  assert.deepEqual(h.tts.spoken(), []);
  assert.equal(h.screen.state().queued.length, 2);

  // The one at the top interrupts, even though it was offered last: the rule is
  // the ladder, not the queue's order.
  h.screen.offer({ text: 'a run failed', severity: SEVERITY.failed });
  assert.deepEqual(h.tts.spoken(), ['a run failed']);
  assert.deepEqual(
    h.screen.state().queued,
    [{ text: 'the burn is fast', severity: 'fast_burn' }, { text: 'a run is running', severity: 'info' }],
    'and the ones below the top are still waiting',
  );

  // When the digest's top falls to `info`, the waiting update that is AT the
  // new top is spoken, and it is spoken in the order it was queued.
  h.tts.finish();
  await settle();
  h.screen.setDigest(digestOf(SEVERITY.info));
  assert.deepEqual(h.tts.spoken(), ['a run failed', 'a run is running']);

  // ... and the one ABOVE the new top is not spoken, and is not dropped
  // either: the rule is the digest's own highest, and a digest's `severity` is
  // the most severe item it has -- so an update above it is an update about
  // something the digest no longer carries. It waits. The queue's bound is
  // what surfaces that (`the queue is bounded, and refuses rather than
  // dropping what is in it`), because a silent drop is the failure this whole
  // module is written against.
  assert.deepEqual(h.screen.state().queued, [{ text: 'the burn is fast', severity: 'fast_burn' }]);
  assert.deepEqual(h.tts.spoken(), ['a run failed', 'a run is running'], 'and nothing else was said');
});

test('a digest that arrives after the queue does not speak an update it does not cover', async () => {
  // The order matters and is not an accident: an update is judged against the
  // digest that is current when the boundary arrives, so an update offered
  // before any digest is refused rather than queued against a rule nobody has.
  const h = await harness({ switch: INTERRUPT_SWITCH.ON });
  assert.deepEqual(
    { ...h.screen.offer({ text: 'a run failed', severity: SEVERITY.failed }) },
    { ok: false, code: 'no-digest', field: 'digest' },
  );
  assert.deepEqual(h.screen.state().queued, []);
});

// --- the plan, sentence two ------------------------------------------------

test('the plan, sentence two: the offline state shows from P1.4, and the app refuses to accept a message', async () => {
  const h = await harness();
  // A mailbox with no heartbeat: P1.4's own answer, read through the client.
  const report = await h.screen.poll();
  assert.equal(report.presence.state, STATE.OFFLINE);
  assert.equal(report.presence.reason, 'no-heartbeat');

  const state = h.screen.state();
  assert.equal(state.offline, true, 'the screen shows it');
  assert.equal(state.presence.state, 'offline', 'from P1.4, with P1.4\'s reason');
  assert.equal(state.presence.reason, 'no-heartbeat');

  // ... and the refusal is the point: the app does not take the message.
  const before = h.writes();
  const accepted = await h.screen.accept('run the tests');
  await settle();
  assert.deepEqual({ ...accepted }, { ok: false, code: 'offline', field: 'presence' });
  assert.equal(h.writes(), before, 'and NOTHING was written: no message into the void');
  assert.deepEqual(h.screen.state().refused, { code: 'offline', field: 'presence' }, 'and the refusal is shown');

  // The message is judged before the presence is: an empty message is refused
  // as an empty message, offline or not, because the code names the fault the
  // caller can fix. Without this the two guards could be swapped and no test
  // would tell -- the client refuses an empty text with the same code.
  assert.deepEqual({ ...(await h.screen.accept('')) }, { ok: false, code: 'bad-text', field: 'text' });
  assert.equal(h.writes(), before, 'and still nothing was written');
});

test('a live orchestrator is what lifts the refusal, and it is P1.4 that says so', async () => {
  const h = await harness();
  await h.screen.poll();
  assert.equal(h.screen.state().offline, true);

  await h.plantHeartbeat();
  const live = await h.screen.poll();
  assert.equal(live.presence.state, STATE.LIVE);
  assert.equal(h.screen.state().offline, false);

  const accepted = await h.screen.accept('run the tests');
  await settle();
  assert.deepEqual({ ...accepted }, { ok: true, id: 'm-1', at: T0 });
  assert.deepEqual(
    h.pathsWritten().filter((path) => path.includes('/inbox/')),
    [`mailbox/${UID}/inbox/m-1`],
    'the message is written, by the client, to the client\'s own path',
  );
  assert.equal(h.screen.state().refused, null, 'and there is no refusal to show');
});

test('the refusal a screen shows is the last one, and the message that is accepted clears it', async () => {
  // The snapshot's `refused` is a state, not a history. A screen that went on
  // rendering `offline` over a message that had just left the phone would be
  // showing a state the phone is not in -- which is the one thing a status line
  // must not do -- and the lease's own refusal is cleared by the call that
  // succeeds for the same reason.
  const h = await harness();
  await h.screen.poll();
  await h.screen.accept('run the tests');
  assert.deepEqual(h.screen.state().refused, { code: 'offline', field: 'presence' }, 'shown while it holds');

  await h.plantHeartbeat();
  await h.screen.poll();
  const accepted = await h.screen.accept('run the tests');
  await settle();
  assert.equal(accepted.ok, true, 'the orchestrator is back, so the message goes');
  assert.equal(h.screen.state().refused, null, 'and the refusal it followed is cleared');
});

test('P1.4\'s answer is adopted through one guard, whether a caller or the poll brings it', async () => {
  const h = await harness();
  // A caller may hand the screen a presence directly -- a screen that reads the
  // node itself, or a test -- and the same guard adopts it.
  assert.deepEqual({ ...h.screen.setPresence({ state: STATE.LIVE, reason: null }) }, { ok: true });
  assert.deepEqual({ ...h.screen.presence() }, { state: STATE.LIVE, reason: null }, 'and the reader answers with it');
  assert.equal(h.screen.state().offline, false);

  // A state outside P1.4's four is refused, and the last real answer stands
  // rather than being replaced by it.
  assert.deepEqual({ ...h.screen.setPresence({ state: 'gone' }) }, { ok: false, code: 'bad-presence', field: 'state' });
  assert.deepEqual({ ...h.screen.presence() }, { state: STATE.LIVE, reason: null });
});

test('the poll\'s presence goes through that same guard, which is the path the app takes', async () => {
  // The real client reports one of P1.4's own four states, so this is the
  // port's contract rather than a case the app meets today -- and a rule
  // checked only on a path the app does not take is a rule the screen does not
  // have. The client's report is returned as itself; what the screen shows is
  // what it adopted, and a refusal it did not choose is shown rather than
  // swallowed.
  const h = await harness();
  const stub = {
    send: async () => Object.freeze({ ok: true }),
    poll: async () => Object.freeze({ ok: true, presence: { state: 'gone', reason: 'nonsense' } }),
  };
  const screen = createVoiceScreen({ pipeline: h.pipeline, client: stub, now: h.now, holder: PHONE });

  const report = await screen.poll();
  assert.equal(report.presence.state, 'gone', 'the client\'s report is the client\'s');
  assert.equal(screen.presence(), null, 'and nothing outside P1.4\'s four is stored');
  assert.deepEqual(screen.state().refused, { code: 'bad-presence', field: 'state' }, 'the screen shows why');
  assert.equal(screen.state().offline, false, 'and an unreadable state is not read as offline');
});

test('stale is not offline for the screen, and P6.10 is what refuses it', async () => {
  // Two rules that are easy to confuse, held apart here.
  //
  // The SCREEN refuses P1.4's `offline` and nothing wider: a stale orchestrator
  // is one whose heartbeat is late, not one that is down, and a status line
  // that widened `offline` to cover it would be showing a state P1.4 does not
  // have.
  //
  // The PHONE refuses it anyway, and that is P6.10's rule rather than this
  // screen's: the plan's P6.10 sentence names **stale** beside offline ("with no
  // connection, or a stale/offline consumer, nothing is written and the user is
  // told"), so the client's gate refuses a stale consumer with its own code and
  // the screen shows the client's code as it is. This test asserted the
  // opposite until the gate was merged in; the P6.10 sentence is what changed
  // it.
  const h = await harness();
  await h.plantHeartbeat();
  h.advance(5 * INTERVAL_MS);
  const stale = await h.screen.poll();
  assert.equal(stale.presence.state, STATE.STALE);
  assert.equal(h.screen.state().offline, false, 'P1.4 has no `stale` and offline means offline');

  // The refusal SPEAKS (`gate.js`'s sentence, through the app's own speaker), so
  // the utterance is played out the way every other one in this file is: the
  // fake tts settles when the test says it does.
  const refused = h.screen.accept('run the tests');
  await settle();
  h.tts.finish();
  const accepted = await refused;
  await settle();
  assert.deepEqual(
    { ...accepted },
    {
      ok: false,
      code: GATE_CODES.CONSUMER_STALE,
      field: 'presence',
      message: 'The orchestrator has gone quiet. I have not sent that.',
      told: true,
    },
    'stale refuses the write, with the gate\'s own code rather than a wider `offline`',
  );
  assert.deepEqual(h.tts.spoken(), ['The orchestrator has gone quiet. I have not sent that.']);
  // `refused` stays null: the screen records ITS OWN refusals (`noteRefusal`),
  // and this one is the client's, returned as the client's -- which is the
  // screen's existing rule and not something P6.10 changes.
  assert.equal(h.screen.state().refused, null);
  // Nothing was written BY THE PHONE: `writes()` counts the fixture's own
  // heartbeat too, so the claim is about the client's path (D113).
  assert.deepEqual(
    h.pathsWritten().filter((path) => path.includes('/inbox/')),
    [],
    'and nothing was written',
  );
});

test('a transcript that arrives while offline is refused too, and nothing is written', async () => {
  // The hands-free half of the same rule: the pipeline composes the message,
  // and the screen is what decides whether the app accepts it. The user's words
  // are refused by the same guard as a typed one -- there is one door.
  const h = await harness();
  await h.screen.poll();
  const before = h.writes();

  await h.screen.start();
  h.audioIn.speechStart();
  h.audioIn.speechEnd();
  h.stt.final('run the tests');
  await settle();

  assert.equal(h.writes(), before, 'nothing reached the store');
  assert.deepEqual(h.screen.state().refused, { code: 'offline', field: 'presence' });
  assert.equal(h.screen.state().transcript.text, 'run the tests', 'and what was heard is still shown');
});

// --- the plan, sentence three ----------------------------------------------

test('the plan, sentence three: a lease held by another consumer is surfaced rather than ignored', async () => {
  const h = await harness();
  // The laptop's consumer holds the lease -- it is the one running the agents,
  // and the phone is the one being spoken to.
  const taken = await h.screen.setLease(emptyLeaseState());
  assert.equal(taken.ok, true);
  const held = h.screen.state().lease;
  assert.equal(held, null, 'nothing is held to begin with');
  assert.deepEqual({ ...h.screen.lease() }, { ok: true, lease: null }, 'and the reader says so in P1.2\'s own shape');

  // The other consumer takes it, in P1.2's own shape, and the phone adopts the
  // state it read out of the database.
  const theirs = takeLease(emptyLeaseState(), { holder: LAPTOP, at: h.now() });
  assert.equal(theirs.ok, true, 'the fixture lease is taken');
  assert.deepEqual({ ...(await h.screen.setLease(theirs.state)) }, { ok: true });

  // Now the phone asks for it, and hears who has it.
  const mine = h.screen.takeLease();
  assert.equal(mine.ok, false);
  assert.equal(mine.code, 'already-claimed');
  assert.deepEqual({ ...mine.existing }, { holder: LAPTOP, at: h.now() });
  assert.match(mine.message, new RegExp(LAPTOP), 'and the message names the holder');
  assert.deepEqual(h.screen.state().lease, { holder: LAPTOP, at: h.now() }, 'surfaced in the view, not swallowed');
  assert.deepEqual({ ...h.screen.lease() }, { ok: true, lease: { holder: LAPTOP, at: h.now() } }, 'and by the reader');
  assert.equal(h.screen.state().leaseRefused.code, 'already-claimed');

  // A release that is not the phone's is refused as itself, and never done.
  const released = h.screen.releaseLease();
  assert.equal(released.ok, false);
  assert.equal(released.code, 'not-holder');
  assert.deepEqual(h.screen.state().lease, { holder: LAPTOP, at: h.now() }, 'the other consumer still holds it');
});

test('a lease the other consumer has let go of is the phone\'s to take, and it is taken', async () => {
  const h = await harness();
  const theirs = takeLease(emptyLeaseState(), { holder: LAPTOP, at: h.now() });
  await h.screen.setLease(theirs.state);
  assert.equal(h.screen.takeLease().ok, false, 'a fresh lease is not taken over');

  // P1.2's TTL: ten minutes later the claim is no longer fresh, and the phone
  // may take it -- which is the half of the rule that keeps a dead laptop from
  // blocking the phone forever.
  h.advance(10 * 60 * 1000);
  const mine = h.screen.takeLease();
  assert.deepEqual({ ...mine.lease }, { holder: PHONE, at: h.now() });
  assert.deepEqual(h.screen.state().lease, { holder: PHONE, at: h.now() });
  assert.equal(h.screen.state().leaseRefused, null, 'and there is nothing to show, because nothing was refused');

  const released = h.screen.releaseLease();
  assert.equal(released.ok, true);
  assert.equal(h.screen.state().lease, null);
});

test('the lease does not gate the app: the phone sends while another consumer holds it', async () => {
  // The plan says the lease is **surfaced**, not enforced, and this is the line
  // that keeps a lease held by the laptop from silencing the phone. A rule that
  // blocked the phone on it would be a policy the plan does not have, and one
  // whose failure mode -- an owner who cannot say anything because a stale
  // laptop holds a lease -- is worse than the one it prevents.
  const h = await harness();
  const theirs = takeLease(emptyLeaseState(), { holder: LAPTOP, at: h.now() });
  await h.screen.setLease(theirs.state);
  await h.plantHeartbeat();
  await h.screen.poll();
  assert.equal(h.screen.takeLease().ok, false);

  const accepted = await h.screen.accept('run the tests');
  await settle();
  assert.deepEqual({ ...accepted }, { ok: true, id: 'm-1', at: T0 });
});

// --- D122: the microphone the OS refused ------------------------------------

test('D122: a refused microphone is shown, and the screen keeps working without it', async () => {
  const h = await harness({ refuse: AUDIO_IN_CODES.MICROPHONE_DENIED });
  assert.deepEqual(h.screen.state().microphone, { ok: true }, 'nothing has been asked for yet');

  const started = await h.screen.start();
  assert.deepEqual({ ...started }, { ok: false, code: 'microphone-denied', field: 'start' });
  assert.deepEqual(
    h.screen.state().microphone,
    { ok: false, code: 'microphone-denied' },
    'the screen shows it, which is what the "tap to speak" control is built on',
  );
  // ... and the rest of the screen is not degraded with it: the queue, the
  // switch and the mailbox all still work.
  h.screen.setDigest(digestOf(SEVERITY.kill));
  h.screen.setSwitch(INTERRUPT_SWITCH.ON);
  h.screen.offer({ text: 'a run was killed', severity: SEVERITY.kill });
  assert.deepEqual(h.tts.spoken(), ['a run was killed']);
});

test('a microphone the OS granted is shown as granted', async () => {
  const h = await harness();
  assert.deepEqual({ ...(await h.screen.start()) }, { ok: true, changed: true });
  assert.deepEqual(h.screen.state().microphone, { ok: true });
  // ... and stopping is not a refusal: the screen shows what the OS answered.
  await h.screen.stop();
  assert.deepEqual(h.screen.state().microphone, { ok: true });
});

// --- the guards -------------------------------------------------------------

test('the queue is bounded, and refuses rather than dropping what is in it', async () => {
  const h = await harness({ switch: INTERRUPT_SWITCH.OFF, queueCap: 2 });
  h.screen.setDigest(digestOf(SEVERITY.info));
  assert.equal(h.screen.offer({ text: 'the first', severity: SEVERITY.info }).ok, true);
  assert.equal(h.screen.offer({ text: 'the second', severity: SEVERITY.info }).ok, true);

  const full = h.screen.offer({ text: 'the third', severity: SEVERITY.info });
  assert.deepEqual({ ...full }, { ok: false, code: 'queue-full', field: 'queue' });
  assert.deepEqual(
    h.screen.state().queued.map((item) => item.text),
    ['the first', 'the second'],
    'the oldest are still there: nothing is evicted to make room',
  );
  assert.ok(QUEUE_CAP >= 2, 'and the default cap is not a one-item queue');
});

test('an update that is not an update, and a switch that is not a switch, are refusals with a code', async () => {
  const h = await harness();
  h.screen.setDigest(digestOf(SEVERITY.kill));

  assert.deepEqual({ ...h.screen.offer(null) }, { ok: false, code: 'bad-update', field: null });
  assert.deepEqual({ ...h.screen.offer({ text: '', severity: 'kill' }) }, { ok: false, code: 'bad-text', field: 'text' });
  assert.deepEqual(
    { ...h.screen.offer({ text: 'a run was killed', severity: 'critical' }) },
    { ok: false, code: 'bad-severity', field: 'severity' },
  );
  assert.deepEqual(
    { ...h.screen.offer({ text: 'a run was killed', severity: 'kill', loud: true }) },
    { ok: false, code: 'unknown-field', field: 'loud' },
  );
  assert.deepEqual(h.screen.state().queued, [], 'and none of them was queued');

  assert.deepEqual({ ...h.screen.setSwitch('sometimes') }, { ok: false, code: 'bad-switch', field: 'switch' });
  assert.equal(h.screen.state().switch, INTERRUPT_SWITCH.OFF, 'the setting is unchanged');

  assert.deepEqual({ ...h.screen.setDigest({ severity: 'critical' }) }, { ok: false, code: 'bad-severity', field: 'severity' });
  assert.deepEqual({ ...h.screen.setDigest(null) }, { ok: false, code: 'bad-digest', field: null });
  assert.deepEqual({ ...h.screen.setPresence({ state: 'gone' }) }, { ok: false, code: 'bad-presence', field: 'state' });
  assert.deepEqual({ ...h.screen.setPresence(null) }, { ok: false, code: 'bad-presence', field: null });

  // ... and a message that is not one is refused by the same door.
  assert.deepEqual({ ...(await h.screen.accept('')) }, { ok: false, code: 'bad-text', field: 'text' });
  assert.deepEqual({ ...(await h.screen.accept(null)) }, { ok: false, code: 'bad-text', field: 'text' });
});

test('a lease state that is not one is refused, and the screen keeps the one it had', async () => {
  const h = await harness();
  const theirs = takeLease(emptyLeaseState(), { holder: LAPTOP, at: h.now() });
  await h.screen.setLease(theirs.state);

  const refused = await h.screen.setLease({ claim: { holder: LAPTOP }, ttl: 0 });
  assert.equal(refused.ok, false);
  assert.equal(refused.code, 'bad-lease');
  assert.deepEqual(h.screen.state().lease, { holder: LAPTOP, at: h.now() }, 'P1.2\'s refusal, and no state lost');
});

test('the screen is built from a pipeline, a client, a clock and a holder, and says so when it is not', async () => {
  const h = await harness();
  const deps = { pipeline: h.pipeline, client: h.client, now: h.now, holder: PHONE };
  assert.throws(() => createVoiceScreen({ ...deps, pipeline: {} }), TypeError);
  assert.throws(() => createVoiceScreen({ ...deps, client: { send: () => {} } }), TypeError);
  assert.throws(() => createVoiceScreen({ ...deps, now: 'now' }), TypeError);
  assert.throws(() => createVoiceScreen({ ...deps, holder: '' }), TypeError);
  assert.throws(() => createVoiceScreen({ ...deps, switch: 'sometimes' }), TypeError);
  assert.throws(() => createVoiceScreen({ ...deps, utterance: 'confirm' }), TypeError);
  assert.throws(() => createVoiceScreen(), TypeError);
});

test('a final transcript goes to the spoken dialogue when the app wires one in (P6.12)', async () => {
  // P6.12's seam: the dialogue decides whether the words are a message, an app
  // command or a confirmation. The screen's `accept` is what the dialogue's own
  // `send` is wired to, so the message path -- and its offline refusal -- is
  // unchanged; what changes is that a final is no longer *always* a message.
  const heard = [];
  const wired = await harness({
    utterance: async (text) => {
      heard.push(text);
      return { ok: true };
    },
  });

  await wired.screen.start();
  wired.audioIn.speechStart();
  wired.audioIn.speechEnd();
  wired.stt.final('Fugaro, confirm');
  await settle();

  assert.deepEqual(heard, ['Fugaro, confirm'], 'the final went to the dialogue');
  assert.deepEqual(wired.pathsWritten().filter((path) => path.includes('/inbox/')), [],
    'and nothing was written: a confirmation is not a message');

  // The same turn without a dialogue is what P3.5 shipped: every final is a
  // message, written by the client.
  const plain = await harness();
  await plain.screen.start();
  plain.audioIn.speechStart();
  plain.audioIn.speechEnd();
  plain.stt.final('Fugaro, confirm');
  await settle();

  assert.deepEqual(plain.pathsWritten().filter((path) => path.includes('/inbox/')),
    [`mailbox/${UID}/inbox/m-1`], 'without a dialogue the words are a message');
});

test('the screen shows what it is doing, in one frozen snapshot', async () => {
  const h = await harness();
  const state = h.screen.state();
  assert.equal(Object.isFrozen(state), true);
  assert.deepEqual(Object.keys(state).sort(), [
    'lease',
    'leaseRefused',
    'microphone',
    'offline',
    'presence',
    'queued',
    'refused',
    'speaking',
    'switch',
    'transcript',
    'turn',
  ]);
  assert.equal(Object.isFrozen(state.queued), true);
  // Nothing has happened yet, and the snapshot says so rather than guessing:
  // no presence is `offline: false`, because P1.4 has not answered.
  assert.deepEqual({ ...state }, {
    switch: 'off',
    presence: null,
    offline: false,
    microphone: { ok: true },
    queued: [],
    lease: null,
    leaseRefused: null,
    speaking: false,
    turn: false,
    transcript: null,
    refused: null,
  });
  // ... and it follows the pipeline rather than caching: the user speaking is
  // in it, and so is the partial they are still saying.
  await h.screen.start();
  h.audioIn.speechStart();
  h.stt.partial('run the');
  assert.equal(h.screen.state().turn, true);
  assert.deepEqual(h.screen.state().transcript, { text: 'run the', final: false });
  h.audioIn.speechEnd();
  h.stt.final('run the tests');
  assert.deepEqual(h.screen.state().transcript, { text: 'run the tests', final: true });
});
