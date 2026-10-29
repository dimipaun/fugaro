// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The spoken confirmation and the routing in the voice screen (mobile plan,
// Task P6.12; D162, D169, D164, D168, D160, D127).
//
// The plan's verification is six sentences, and this file is those six, in
// order, over the audio fakes -- no device, no network, no database, no
// microphone (Global Constraint 10):
//
//   1. *a destructive command produces the read-back from the resolved target
//      and waits;*
//   2. *a confirm in the same utterance is refused;*
//   3. *a misheard confirm is refused;*
//   4. *a timeout says so and drops the action;*
//   5. *a read-back for a merge states the PR's **current** head at confirmation
//      time (D168: a prompt can be answered days later);*
//   6. *and a queued prompt is not spoken over the user.*
//
// The pipeline and the three audio ports are P3.4's real ones, built the way the
// app builds them, and the router and the machine are P6.1's and P6.2's real
// modules (merged). `speak()` is not stubbed out: the read-back is asserted on
// **what the output port was handed** (`tts.spoken()`), so "the read-back was
// spoken" is a claim about the fake's own log rather than about a return value.
//
// The two failure modes the task is security-sensitive for are their own tests,
// and the first of them is the one the mutation harness proves:
//
//   * **an unheard read-back cannot be confirmed** -- the speech is cancelled
//     part-way (the user barged in, or the port refused it) and the phrase said
//     afterwards authorises nothing, because the action was dropped when its
//     read-back did not play out;
//   * **nothing is sent on an unconfirmed transcript** -- `send` is a spy, and
//     every test that proposes, confirms or expires a destructive command
//     asserts that it was never called, so a destructive command can never
//     travel as its own text (D169). The only things the spy ever receives are
//     utterances the router read as agent text and the one launch D162 passes
//     through, and the tests that see it called assert its contents exactly.
//
// Every secret-shaped string below is synthetic and is not recorded from any
// installation (`belong` least of all, Global Constraint 11).

import assert from 'node:assert/strict';
import test from 'node:test';

import { REFUSAL as CONFIRM_REFUSAL } from '../../../shared/confirm/index.js';
import { SWITCH, enqueue, initialQueue } from '../../../shared/prompts/index.js';
import { FakeAudioIn } from '../audio/audio-in.js';
import { createVoicePipeline } from '../audio/pipeline.js';
import { FakeStt } from '../audio/stt.js';
import { FakeTts } from '../audio/tts.js';
import { DIALOGUE_CODES, DIALOGUE_OUTCOME, createVoiceDialogue } from './dialogue.js';

/** A mailbox id shaped like an Auth uid, and never a real one. */
const UID = 'AbCdEf0123456789ghIJkl';

const T0 = 1_700_000_000_000;

/** The fixed phrase, exactly, in P6.2's own words. */
const PHRASE = 'Fugaro, confirm';

/** P6.2's own sentence, which every read-back ends with. */
const CUE = 'Say "Fugaro, confirm" to confirm.';

/** A run id in P6.3's alphabet: what a cancel cancels. */
const RUN = Object.freeze({ kind: 'run', runId: 'run-42' });

/** A merge target: the repository, the PR, and the head that was read back. */
const mergeAt = (headSha) => Object.freeze({ kind: 'merge', repo: 'fugaro/app', pr: 7, headSha });

/** Two heads for one PR: the one read back, and the one that arrived after. */
const HEAD_THEN = 'a'.repeat(40);
const HEAD_NOW = 'b'.repeat(40);

/** A prompt the orchestrator wrote, shaped and signed as P0.2's schema requires. */
const PROMPT = Object.freeze({
  id: 'p1',
  sender: 'agent-1',
  text: 'Deploy the release?',
  at: T0,
  mac: 'a'.repeat(64),
  kind: 'prompt',
});

/**
 * The voice path with the device replaced: P3.4's pipeline over its three fakes,
 * and this task's dialogue over it. `now` is a variable rather than a clock --
 * nothing here reads the time, and a test that wants "an hour later" says so.
 *
 * `send` records what was handed to it, which is how "nothing was sent" is
 * asserted: it is the app's one write path (P3.6), wired the way the screen
 * wires it (`screen.accept`), and a test that replaces it can also make it
 * refuse -- P1.4's offline case.
 */
function harness(options = {}) {
  const audioIn = new FakeAudioIn();
  const stt = new FakeStt();
  const tts = new FakeTts();
  let nowMs = T0;
  let ids = 0;
  const pipeline = createVoicePipeline({
    audioIn,
    stt,
    tts,
    sender: UID,
    newId: () => `p-${++ids}`,
    now: () => nowMs,
  });
  const sent = [];
  const send = options.send === undefined
    ? async (text) => {
      sent.push(text);
      return { ok: true, id: `m-${sent.length}`, at: nowMs };
    }
    : options.send;
  const dialogue = createVoiceDialogue({
    pipeline,
    send,
    now: () => nowMs,
    newId: () => `d-${++ids}`,
    ...(options.router === undefined ? {} : { router: options.router }),
    ...(options.confirm === undefined ? {} : { confirm: options.confirm }),
  });
  return {
    audioIn,
    stt,
    tts,
    pipeline,
    dialogue,
    sent,
    setNow: (value) => {
      nowMs = value;
    },
    advance: (ms) => {
      nowMs += ms;
    },
    now: () => nowMs,
  };
}

/**
 * Run a call to its spoken end: the fake's playback finishes first, which is
 * what makes the promise settle. `speak()` hands the text to the port before the
 * caller's line runs, so the finish is not racing anything.
 */
async function spoken(call, h) {
  h.tts.finish();
  return await call;
}

/**
 * Run a call whose sentence was **interrupted**: the user barged in, or the
 * output port refused it. P3.4's port reports both as a refusal.
 */
async function interrupted(call, h) {
  h.tts.cancel();
  return await call;
}

/** A queue with one question in it, built the way the app builds it (P6.5). */
function queued(h, { account = 'laptop', prompt = PROMPT } = {}) {
  const opened = initialQueue({ now: h.now(), account });
  assert.equal(opened.ok, true, 'the fixture queue is valid');
  const added = enqueue(opened.queue, { now: h.now(), prompt, destructive: true });
  assert.equal(added.ok, true, 'the fixture prompt is queued');
  return added.queue;
}

// --- the wiring ------------------------------------------------------------

test('the wiring is refused when a dialogue could not work', () => {
  const audioIn = new FakeAudioIn();
  const pipeline = createVoicePipeline({
    audioIn,
    stt: new FakeStt(),
    tts: new FakeTts(),
    sender: UID,
    newId: () => 'p-1',
    now: () => T0,
  });
  const ports = { pipeline, send: async () => ({ ok: true, id: 'm-1', at: T0 }), now: () => T0, newId: () => 'd-1' };

  // A wake word P6.2 does not recognise beside `confirm` would accept every
  // command and refuse the phrase that authorises an action: the owner would
  // learn it mid-action, so it is a wiring failure rather than a surprise.
  assert.throws(
    () => createVoiceDialogue({ ...ports, router: { aliases: ['jarvis'] } }),
    /no configured wake word can confirm/,
  );
  // The machine's state carries a watermark, so a clock that is not a time could
  // never judge anything against it.
  assert.throws(() => createVoiceDialogue({ ...ports, now: () => 0 }), /not a time/);
  assert.throws(() => createVoiceDialogue({ ...ports, pipeline: {} }), /pipeline/);
  assert.throws(() => createVoiceDialogue({ ...ports, send: 'text' }), /sink/);
  // The machine's own refusal, carried as properties so the message echoes
  // nothing that was in the options.
  assert.throws(
    () => createVoiceDialogue({ ...ports, confirm: { timeoutMs: 1 } }),
    (error) => error.code === 'invalid-timeout' && error.field === 'timeoutMs',
  );
});

// --- the plan, sentence one ------------------------------------------------

test('the plan, sentence one: a destructive command speaks the read-back from the resolved target and waits', async () => {
  const h = harness();

  const answer = await spoken(h.dialogue.propose({
    text: 'Fugaro, cancel run 42',
    verb: 'cancel',
    target: RUN,
    actionId: 'a-1',
  }), h);

  assert.equal(answer.ok, true);
  assert.equal(answer.outcome, DIALOGUE_OUTCOME.READ_BACK, 'nothing is authorised by the command itself');
  assert.equal(answer.actionId, 'a-1');
  assert.equal(answer.deadline, T0 + 60_000, "the timeout is P6.2's own, measured from the command");
  assert.equal(answer.target.runId, RUN.runId);
  // The sentence the output port was handed, in the machine's own words.
  assert.deepEqual(h.tts.spoken(), [`Cancel run run-42. ${CUE}`]);
  assert.deepEqual(answer.readBack, h.tts.spoken()[0]);

  // ... and then it waits: the read-back is what the phrase will be about.
  const waiting = h.dialogue.pending();
  assert.equal(waiting.ok, true);
  assert.equal(waiting.actions.length, 1);
  assert.equal(waiting.actions[0].actionId, 'a-1');
  assert.equal(waiting.actions[0].readBack, `Cancel run run-42. ${CUE}`);
  assert.equal(waiting.actions[0].deadline, T0 + 60_000);

  // A wait is not a send: the command has not travelled anywhere yet.
  assert.deepEqual(h.sent, []);
});

test('the read-back names the resolved target, never the words the speaker used', async () => {
  const h = harness();

  // The speaker called the run "the docs refactor" and the resolved target is
  // run-42. D47's Rule is that the read-back names the run id, because a
  // paraphrase would be confirmed as the paraphrase.
  const answer = await spoken(h.dialogue.propose({
    text: 'Fugaro, cancel the docs refactor',
    verb: 'cancel',
    target: RUN,
    actionId: 'a-1',
  }), h);

  assert.equal(answer.readBack, `Cancel run run-42. ${CUE}`);
  assert.equal(answer.readBack.includes('docs refactor'), false);
});

test('a spoken destructive command is not understood, and is never forwarded (D169, D164)', async () => {
  const h = harness();

  // P6.1's grammar has no destructive row at all, deliberately: free text
  // cannot reach a cancel, a merge or a confirmation. This is the utterance a
  // speaker who expected otherwise would say.
  const answer = await spoken(h.dialogue.utterance('Fugaro, cancel run 42'), h);

  assert.equal(answer.ok, true);
  assert.equal(answer.outcome, DIALOGUE_OUTCOME.NOT_UNDERSTOOD);
  assert.equal(answer.reason, 'unmatched');
  assert.deepEqual(h.tts.spoken(), ["I didn't catch that"], 'the router’s own sentence is what is spoken');
  assert.deepEqual(h.sent, [], 'and a wake-word utterance is never forwarded as agent text');
  assert.deepEqual(h.dialogue.pending().actions, [], 'nothing is pending, so nothing can be confirmed');
});

// --- the plan, sentence two ------------------------------------------------

test('the plan, sentence two: a confirm in the same utterance as the command is refused', async () => {
  const h = harness();

  const answer = await h.dialogue.propose({
    text: 'Fugaro, cancel run 42 and confirm',
    verb: 'cancel',
    target: RUN,
    actionId: 'a-1',
  });

  // P6.2's `command-and-confirm`: one utterance that asks and answers is not a
  // confirmation, whatever it says. Nothing was spoken, nothing is pending, and
  // nothing was sent.
  assert.deepEqual({ ...answer }, { ok: false, code: CONFIRM_REFUSAL.COMMAND_AND_CONFIRM, field: 'text' });
  assert.deepEqual(h.tts.spoken(), []);
  assert.deepEqual(h.dialogue.pending().actions, []);
  assert.deepEqual(h.sent, []);
});

// --- the plan, sentence three ----------------------------------------------

test('the plan, sentence three: a misheard confirm is refused', async () => {
  const h = harness();

  const read = await spoken(h.dialogue.propose({
    text: 'Fugaro, cancel run 42',
    verb: 'cancel',
    target: RUN,
    actionId: 'a-1',
  }), h);
  assert.equal(read.outcome, DIALOGUE_OUTCOME.READ_BACK);

  // "confirm it" and "conform": the wake word is there, so P6.1 looks for an app
  // command, finds none, and answers with its own sentence -- never forwarding
  // the words and never repairing a misrecognition into a confirmation.
  for (const misheard of ['Fugaro, confirm it', 'Fugaro, conform']) {
    const answer = await spoken(h.dialogue.utterance(misheard), h);
    assert.equal(answer.outcome, DIALOGUE_OUTCOME.NOT_UNDERSTOOD, `"${misheard}" is not the phrase`);
    assert.equal(h.dialogue.pending().actions.length, 1, `"${misheard}" authorised nothing`);
    assert.deepEqual(h.sent, [], 'and a wake-word utterance is not forwarded');
  }

  // A bare "yes" is agent text -- P6.1 forwards it, D164 -- so the agent may
  // receive it; what it must not do is confirm anything here.
  const yes = await h.dialogue.utterance('yes');
  assert.equal(yes.outcome, DIALOGUE_OUTCOME.SENT);
  assert.deepEqual(h.sent, ['yes']);
  assert.equal(h.dialogue.pending().actions.length, 1, '"yes" authorised nothing either');
  assert.deepEqual(h.tts.spoken(), [`Cancel run run-42. ${CUE}`, "I didn't catch that", "I didn't catch that"], 'no second read-back was spoken');

  // The phrase itself is what confirms -- and only after the read-back.
  const confirmed = await h.dialogue.utterance(PHRASE);
  assert.equal(confirmed.outcome, DIALOGUE_OUTCOME.CONFIRMED);
  assert.equal(confirmed.record.id, 'a-1');
});

// --- the plan, sentence four -----------------------------------------------

test('the plan, sentence four: a timeout says so and drops the action', async () => {
  const h = harness();

  await spoken(h.dialogue.propose({
    text: 'Fugaro, cancel run 42',
    verb: 'cancel',
    target: RUN,
    actionId: 'a-1',
  }), h);

  // A prompt can be answered days later (D168) and a read-back cannot wait that
  // long: at the deadline exactly it is still live -- P6.2's boundary -- and a
  // millisecond later it is not.
  h.setNow(T0 + 60_000);
  assert.equal(h.dialogue.pending().actions.length, 1, 'the deadline itself is still inside');
  h.setNow(T0 + 60_001);

  const answer = await h.dialogue.utterance(PHRASE);
  assert.deepEqual({ ...answer }, { ok: false, code: CONFIRM_REFUSAL.CONFIRMATION_EXPIRED, field: 'now' });
  assert.deepEqual(h.dialogue.pending().actions, [], 'and the action is dropped: it is not listed any more');
  assert.deepEqual(h.sent, [], 'a timed-out action sends nothing');

  // Dropped means dropped: the phrase said again is about nothing at all.
  const again = await h.dialogue.utterance(PHRASE);
  assert.equal(again.code, CONFIRM_REFUSAL.NO_PENDING_ACTION);
  assert.deepEqual(h.tts.spoken(), [`Cancel run run-42. ${CUE}`], 'nothing was spoken for the refused confirmations');
});

// --- the plan, sentence five -----------------------------------------------

test('the plan, sentence five: a merge read back at one head speaks the head that is there now', async () => {
  const h = harness();

  const read = await spoken(h.dialogue.propose({
    text: 'Fugaro, merge PR 7',
    verb: 'merge',
    target: mergeAt(HEAD_THEN),
    actionId: 'a-1',
  }), h);
  assert.equal(read.readBack, `Merge fugaro/app#7 at head ${HEAD_THEN}. ${CUE}`);
  assert.equal(read.retargeted, undefined);

  // The PR gained a commit while the question waited. The caller re-reads the
  // head and passes the target **as it is now**; the machine refuses the moved
  // target, and the dialogue answers by proposing the command again against it.
  const moved = await spoken(h.dialogue.utterance(PHRASE, { target: mergeAt(HEAD_NOW) }), h);

  assert.equal(moved.outcome, DIALOGUE_OUTCOME.READ_BACK, 'the first phrase authorised nothing');
  assert.equal(moved.retargeted, true, 'the stale read-back was dropped and a fresh one spoken');
  assert.equal(moved.readBack, `Merge fugaro/app#7 at head ${HEAD_NOW}. ${CUE}`);
  assert.deepEqual(h.tts.spoken(), [
    `Merge fugaro/app#7 at head ${HEAD_THEN}. ${CUE}`,
    `Merge fugaro/app#7 at head ${HEAD_NOW}. ${CUE}`,
  ], 'the speaker was told which head they were confirming, twice');
  assert.deepEqual(h.sent, [], 'nothing was merged and nothing was sent');
  assert.deepEqual(h.dialogue.pending().actions.map((one) => one.target.headSha), [HEAD_NOW]);

  // Now the phrase, with no context to add: it confirms the head that was read
  // back second -- the one the speaker heard -- and the record binds that head.
  const confirmed = await h.dialogue.utterance(PHRASE);
  assert.equal(confirmed.outcome, DIALOGUE_OUTCOME.CONFIRMED);
  assert.equal(confirmed.record.head_sha, HEAD_NOW);
  assert.equal(confirmed.record.target.repo, 'fugaro/app');
  assert.equal(confirmed.record.target.pr, 7);
  assert.equal(confirmed.record.confirmation.id.length > 0, true);
});

// --- the plan, sentence six ------------------------------------------------

test('the plan, sentence six: a queued prompt is not spoken over the user', async () => {
  const h = harness();
  const queue = queued(h);

  // The user is speaking (the engine's turn is open): nothing is said, and the
  // question is not marked heard -- the caller is handed no queue at all.
  assert.equal((await h.pipeline.start()).ok, true, 'the microphone is running');
  h.audioIn.speechStart();
  assert.equal(h.pipeline.turn(), true, 'the turn is open, so a pause is not available');
  const speaking = await h.dialogue.pause({ queues: [queue], switch: SWITCH.WIDEN });
  assert.equal(speaking.outcome, DIALOGUE_OUTCOME.HELD);
  assert.equal(speaking.held, 'user-turn');
  assert.equal(speaking.spoken, false);
  assert.deepEqual(h.tts.spoken(), []);

  // The same while the assistant itself is mid-sentence.
  h.audioIn.speechEnd();
  h.stt.final('run the tests');
  assert.equal(h.pipeline.turn(), false, 'the final is what closes the turn');
  const sentence = h.pipeline.say('Run 42 finished.');
  const overItself = await h.dialogue.pause({ queues: [queue], switch: SWITCH.WIDEN });
  assert.equal(overItself.held, 'speaking');
  assert.deepEqual(h.tts.spoken(), ['Run 42 finished.'], 'the question was not spoken over the sentence');
  h.tts.finish();
  assert.equal((await sentence).ok, true);

  // And while a read-back is outstanding -- the user still has to answer the
  // question they were just asked.
  await spoken(h.dialogue.propose({
    text: 'Fugaro, cancel run 42',
    verb: 'cancel',
    target: RUN,
    actionId: 'a-1',
  }), h);
  const waiting = await h.dialogue.pause({ queues: [queue], switch: SWITCH.WIDEN });
  assert.equal(waiting.held, 'confirmation-pending');
  assert.deepEqual(h.tts.spoken(), ['Run 42 finished.', `Cancel run run-42. ${CUE}`]);

  // A real pause, with nothing else happening: one sentence, and the question is
  // marked heard in the queue the caller keeps.
  h.setNow(T0 + 60_001);
  const at = h.now();
  const announced = await spoken(h.dialogue.pause({ queues: [queue], switch: SWITCH.WIDEN }), h);
  assert.equal(announced.outcome, DIALOGUE_OUTCOME.ANNOUNCED);
  assert.equal(announced.text, 'One question waiting', 'P6.5’s own sentence for the count');
  assert.equal(announced.spoken, true);
  assert.equal(announced.count, 1);
  assert.equal(announced.reason, 'prompt');
  assert.equal(announced.marked, true, 'the questions’ own announcement is what was spoken');
  assert.equal(announced.queue.prompts[0].announcedAt, at, 'the question has been heard as of this pause');
  assert.deepEqual(h.sent, []);
});

test('the pause speaks the interrupt policy’s reason, in the phone’s words', async () => {
  const h = harness();
  const queue = queued(h);

  const announced = await spoken(h.dialogue.pause({
    queues: [queue],
    digest: { severity: 'kill' },
  }), h);

  assert.equal(announced.reason, 'kill');
  assert.equal(announced.text, 'A kill switch is set.');
  assert.deepEqual(h.tts.spoken(), ['A kill switch is set.']);

  // The sentence was the kill switch's, not the questions' -- it says nothing
  // about what is waiting, so the questions are not recorded as heard.
  assert.equal(announced.marked, false, 'a reason sentence is not the questions’ announcement');
  assert.equal(announced.queue.prompts[0].announcedAt, null, 'the question is still unanswered and unheard');
  assert.equal(announced.count, 1, 'and it is still waiting');
});

test('a sentence that did not play out marks nothing heard', async () => {
  const h = harness();
  const queue = queued(h);
  const at = h.now();

  // The user talks over it: P3.4's output port cancels the utterance, so the
  // count was never heard -- and `announcedAt` is when the user heard it.
  const bargedIn = await interrupted(h.dialogue.pause({ queues: [queue], switch: SWITCH.WIDEN }), h);
  assert.equal(bargedIn.outcome, DIALOGUE_OUTCOME.ANNOUNCED);
  assert.equal(bargedIn.text, 'One question waiting');
  assert.equal(bargedIn.spoken, false);
  assert.equal(bargedIn.marked, false, 'a refused or cancelled sentence marks nothing');
  assert.equal(bargedIn.queue.prompts[0].announcedAt, null);

  assert.deepEqual(h.tts.interrupted(), ['One question waiting'], 'the barge-in is what stopped it');

  // And the question is announced at the next pause that does play out.
  const announced = await spoken(h.dialogue.pause({ queues: [bargedIn.queue], switch: SWITCH.WIDEN }), h);
  assert.equal(announced.marked, true);
  assert.equal(announced.queue.prompts[0].announcedAt, at);
});

// --- the headline: fail closed ---------------------------------------------

test('an unheard read-back cannot be confirmed, and nothing is sent on an unconfirmed transcript', async () => {
  const h = harness();

  // The speaker barged in (or the output port refused the sentence): the
  // read-back did not play to the end, so the action it named is dropped before
  // this call answers. Leaving it pending would make the fixed phrase a
  // confirmation of something the speaker was never told.
  const unheard = await interrupted(h.dialogue.propose({
    text: 'Fugaro, cancel run 42',
    verb: 'cancel',
    target: RUN,
    actionId: 'a-1',
  }), h);

  assert.deepEqual({ ...unheard }, { ok: false, code: DIALOGUE_CODES.READ_BACK_NOT_SPOKEN, field: 'pipeline' });
  assert.deepEqual(h.dialogue.pending().actions, [], 'nothing is left to confirm');
  assert.deepEqual(h.sent, []);

  // So the phrase said next authorises nothing at all. (This is the assertion
  // the mutation harness proves: an implementation that keeps the pending
  // read-back answers `confirmed` here, and a cancel of a run the speaker never
  // heard described would have been authorised.)
  const answer = await h.dialogue.utterance(PHRASE);
  assert.equal(answer.ok, false);
  assert.equal(answer.code, CONFIRM_REFUSAL.NO_PENDING_ACTION);
  assert.deepEqual(h.sent, [], 'and still nothing is sent');

  // The rest of the unconfirmed path, on one spy: none of it sends anything.
  // A command is not a message, a confirmation is a record, and text that is
  // not routed as agent text stays unsent (D164).
  const command = await spoken(h.dialogue.propose({
    text: 'Fugaro, merge PR 7',
    verb: 'merge',
    target: mergeAt(HEAD_THEN),
    actionId: 'a-2',
  }), h);
  assert.equal(command.outcome, DIALOGUE_OUTCOME.READ_BACK);
  const confirmed = await h.dialogue.utterance(PHRASE);
  assert.equal(confirmed.outcome, DIALOGUE_OUTCOME.CONFIRMED);
  // The confirmed action is a value, not a write: the merged client's one send
  // writes text messages only and cannot carry an action (P6.9 says the same).
  assert.equal(confirmed.delivered, false);
  assert.equal(confirmed.code, DIALOGUE_CODES.NO_ACTION_WRITE);
  assert.deepEqual(h.sent, []);

  // A replayed phrase authorises nothing: the confirmation id and the action id
  // were spent, and the read-back is gone.
  const replay = await h.dialogue.utterance(PHRASE);
  assert.equal(replay.code, CONFIRM_REFUSAL.NO_PENDING_ACTION);
  assert.deepEqual(h.sent, []);

  // An app command is never forwarded as text either.
  const elsewhere = await h.dialogue.utterance('Fugaro, list projects');
  assert.equal(elsewhere.outcome, DIALOGUE_OUTCOME.ELSEWHERE);
  assert.deepEqual(h.sent, []);
});

// --- the four routes, and the refusals -------------------------------------

test('the three routes are kept apart: agent text is sent, an app command is not, an unmatched word is not', async () => {
  const h = harness();

  const agent = await h.dialogue.utterance('hello there');
  assert.equal(agent.outcome, DIALOGUE_OUTCOME.SENT);
  assert.deepEqual(h.sent, ['hello there']);
  assert.equal(typeof agent.id, 'string');

  // "confirm" with no wake word: P6.1 forwards it (D164), and a machine that
  // also read it as a confirmation would act on words the agent received too.
  const bare = await h.dialogue.utterance('confirm');
  assert.equal(bare.outcome, DIALOGUE_OUTCOME.SENT);
  assert.deepEqual(h.sent, ['hello there', 'confirm']);
  assert.deepEqual(h.dialogue.pending().actions, []);

  const pendingQuestion = await h.dialogue.utterance("Fugaro, what's pending");
  assert.equal(pendingQuestion.outcome, DIALOGUE_OUTCOME.PENDING);
  assert.deepEqual(h.sent, ['hello there', 'confirm']);

  // A launch is D162's: no read-back, nothing to confirm -- and it travels, as
  // the owner's own words, exactly as P6.9's typed path sends it.
  const launch = await h.dialogue.propose({ text: 'Fugaro, launch the release', verb: 'launch' });
  assert.equal(launch.outcome, DIALOGUE_OUTCOME.SENT);
  assert.equal(launch.verb, 'launch');
  assert.equal(launch.actionId, null, 'a launch is never proposed for confirmation');
  assert.deepEqual(h.tts.spoken(), [], 'a launch is not read back');
  assert.deepEqual(h.sent, ['hello there', 'confirm', 'Fugaro, launch the release']);
  assert.deepEqual(h.dialogue.pending().actions, [], 'a launch leaves nothing pending');
});

test('a launch is not confirmed and does travel: it goes as the owner’s own words (D162)', async () => {
  const h = harness();

  // The command the app has resolved is a launch, which is P6.2's one
  // pass-through verb: no target to resolve, no action id, nothing to read back.
  const launch = await h.dialogue.propose({ text: 'Fugaro, launch the release', verb: 'launch' });
  assert.equal(launch.outcome, DIALOGUE_OUTCOME.SENT);
  assert.equal(launch.verb, 'launch');
  assert.equal(launch.actionId, null);
  assert.equal(typeof launch.id, 'string', 'the sink’s own message id');
  assert.deepEqual(h.sent, ['Fugaro, launch the release'], 'the words the owner said, unchanged');
  assert.deepEqual(h.tts.spoken(), [], 'a launch has no destructive effect to read back');
  assert.deepEqual(h.dialogue.pending().actions, [], 'and nothing is left waiting for a phrase');

  // The sink is the app's one write path, so its refusal stands over a launch
  // too -- and the launch does not travel by another route when it refuses.
  const offline = harness({ send: async () => ({ ok: false, code: 'offline', field: null }) });
  const refused = await offline.dialogue.propose({ text: 'Fugaro, launch the release', verb: 'launch' });
  assert.deepEqual({ ...refused }, { ok: false, code: 'offline', field: null });
  assert.deepEqual(offline.tts.spoken(), []);
});

test('the offline refusal stays the client’s, and the command still does not travel', async () => {
  // P1.4's own refusal, from the sink the app wires to the screen's `accept`.
  const h = harness({ send: async () => ({ ok: false, code: 'offline', field: null }) });

  const answer = await h.dialogue.utterance('hello there');
  assert.deepEqual({ ...answer }, { ok: false, code: 'offline', field: null });
});

test('cancel that drops the read-back, and bad input is refused without throwing', async () => {
  const h = harness();

  const nothing = await h.dialogue.utterance('Fugaro, cancel that');
  assert.equal(nothing.outcome, DIALOGUE_OUTCOME.NOTHING_PENDING);
  assert.deepEqual(nothing.actionIds, []);

  const read = await spoken(h.dialogue.propose({
    text: 'Fugaro, cancel run 42',
    verb: 'cancel',
    target: RUN,
    actionId: 'a-1',
  }), h);
  assert.equal(read.outcome, DIALOGUE_OUTCOME.READ_BACK);

  const dropped = await h.dialogue.utterance('Fugaro, cancel that');
  assert.equal(dropped.outcome, DIALOGUE_OUTCOME.DISCARDED);
  assert.deepEqual(dropped.actionIds, ['a-1']);
  assert.deepEqual(h.dialogue.pending().actions, []);
  assert.deepEqual(h.tts.spoken(), [`Cancel run run-42. ${CUE}`], 'dropping a read-back needs no sentence');

  // Bad input: the field set is the version, and nothing echoes what was in the
  // value. A refusal, never a throw, and never a silent success.
  assert.deepEqual({ ...(await h.dialogue.utterance('')) }, { ok: false, code: DIALOGUE_CODES.BAD_TEXT, field: 'text' });
  assert.deepEqual({ ...(await h.dialogue.utterance(42)) }, { ok: false, code: DIALOGUE_CODES.BAD_TEXT, field: 'text' });
  assert.deepEqual(
    { ...(await h.dialogue.utterance(PHRASE, { head: HEAD_NOW })) },
    { ok: false, code: DIALOGUE_CODES.UNKNOWN_FIELD, field: 'head' },
  );
  assert.deepEqual(
    { ...(await h.dialogue.propose({ text: 'x', verb: 'cancel', target: RUN, actionId: 'a-9', force: true })) },
    { ok: false, code: DIALOGUE_CODES.UNKNOWN_FIELD, field: 'force' },
  );
  assert.deepEqual(
    { ...(await h.dialogue.propose({ text: 'x', verb: 'kill', target: RUN, actionId: 'a-9' })) },
    { ok: false, code: CONFIRM_REFUSAL.REFUSED_VERB, field: 'verb' },
  );

  // A clock that is not a time is refused rather than stamped: the machine
  // watermark cannot be advanced, and a confirmation would be judged against a
  // value that is not a moment.
  const before = h.now();
  h.setNow(0);
  assert.deepEqual({ ...(await h.dialogue.propose({ text: 'x', verb: 'cancel', target: RUN, actionId: 'a-10' })) }, {
    ok: false,
    code: DIALOGUE_CODES.BAD_CLOCK,
    field: 'now',
  });
  assert.deepEqual({ ...h.dialogue.pending() }, { ok: false, code: DIALOGUE_CODES.BAD_CLOCK, field: 'now' });
  h.setNow(before);
});
