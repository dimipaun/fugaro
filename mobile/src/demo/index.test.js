// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The demo mode's behaviour: the app's rules, on sample data (owner ruling of
// 2026-10-19).
//
// These tests are written against what a REVIEWER sees, and every one of them
// is meant to fail if the demo stops running the app's own code and starts
// answering on its own:
//
//   * the utterances are really sent (their ids are the client's, and the
//     replies the sample data names are cleared);
//   * the messages are really verified, spoken and acked (`transport.test.js`
//     shows the other half: a wrongly tagged one is dropped);
//   * the question is P6.5's queue and the read-back is P6.2's string, built
//     from the resolved target -- not the sentence the sample data holds;
//   * the confirmation is a real utterance with a fresh id, spent once.
//
// The safety fences -- no network, no secure storage, no secret -- are
// `safety.test.js`'s, and the sample data's own shape is `sample.test.js`'s.

import assert from 'node:assert/strict';
import test from 'node:test';

import { readBack } from '../../../shared/confirm/index.js';
import { createDemoMode, DEMO_BANNER, LEAVE_DEMO, TRY_DEMO } from './index.js';
import {
  DEMO_ACCOUNT,
  DEMO_DESTRUCTIVE,
  DEMO_NOW,
  DEMO_PROMPT,
  DEMO_REPLIES,
  DEMO_UTTERANCES,
} from './sample.js';

/** The demo, entered. */
async function entered(options = {}) {
  const demo = createDemoMode(options);
  const state = await demo.enter();
  return { demo, state };
}

test('the demo is off until it is entered: nothing is built, nothing is shown', () => {
  // A demo that ran on import would be a demo that had already opened a client,
  // a store and a queue in every process that read this module -- including the
  // app's own startup.
  const demo = createDemoMode();
  assert.equal(demo.active(), false);
  assert.equal(demo.state().active, false);
  assert.deepEqual(demo.state().messages, []);
  assert.deepEqual(demo.state().installations, []);
  assert.equal(demo.state().destructive, null);
});

test('entering the demo runs the app\'s own pass: send, verify, speak, ack', async () => {
  const { demo, state } = await entered();
  assert.equal(demo.active(), true);

  // The three utterances were SENT BY THE CLIENT: their ids are the ids the
  // client's own source minted (the demo's fixed counter), and every send
  // landed.
  assert.deepEqual(state.sent.map((entry) => entry.text), [...DEMO_UTTERANCES, DEMO_DESTRUCTIVE.command]);
  assert.deepEqual(state.sent.map((entry) => entry.id), ['demo-utterance-1', 'demo-utterance-2', 'demo-utterance-3']);
  assert.deepEqual(state.sent.filter((entry) => entry.ok !== true), []);

  // **What the phone is still holding, and why.** P6.10's gate refuses a send
  // to a consumer nobody has looked at, so the pass polls BEFORE it sends -- and
  // a verified reply is what retires the phone's own utterance from its outbox,
  // so that look is over before there is anything of the phone's for the replies
  // to answer. `pending()` is therefore the three utterances this pass wrote,
  // landed (the store took them; the consumer has not answered). It was empty
  // when the single poll came last. Both are real states; the demo runs one pass
  // and shows this one. Asserted so the state is pinned rather than discovered:
  // `landed`, not `false`, is the point -- nothing here is waiting to be
  // re-sent, the phone is waiting to be answered.
  assert.deepEqual(state.pending.map((entry) => entry.id), ['demo-utterance-1', 'demo-utterance-2', 'demo-utterance-3']);
  assert.deepEqual(state.pending.filter((entry) => entry.landed !== true), []);
  // ...and the replies really are answers to two of them: the ids the sample
  // `reply_to` fields name are ids the CLIENT minted above, which is the join
  // that makes them answers rather than two unrelated strings.
  const minted = state.sent.map((entry) => entry.id);
  for (const reply of DEMO_REPLIES) assert.ok(minted.includes(reply.replyTo), `${reply.id} names an id nobody sent`);

  // Every message the consumer wrote is spoken and acked: acking is the app's
  // "don't say it twice" ledger, and it can only land after the words were
  // spoken and the tag checked.
  assert.equal(state.messages.length, DEMO_REPLIES.length + 1);
  for (const message of state.messages) {
    assert.equal(message.dropped, null, `${message.id} was dropped`);
    assert.equal(message.spoken, true, `${message.id} was not spoken`);
    assert.equal(message.acked, true, `${message.id} was not acked`);
  }
  const spoken = state.spoken;
  for (const reply of DEMO_REPLIES) assert.ok(spoken.includes(reply.text), reply.id);
  assert.ok(spoken.includes(DEMO_PROMPT.text));

  // The presence rule (P1.4) read the orchestrator's real heartbeat: the demo
  // is live, and the agent it advertises is the demo's.
  assert.equal(state.presence.state, 'live');
  assert.deepEqual(state.presence.agents, [{ id: 'demo-agent', name: 'Demo agent' }]);

  // Nothing refused anywhere: a demo whose sample data made the client refuse
  // something would be showing the refusals and calling them the demo.
  assert.deepEqual(state.problems, []);
});

test('the read-back is the machine\'s, built from the target and not from the command', async () => {
  // D169: the second pass hears the run id itself, so the read-back is not the
  // sentence that produced it. Asserting the demo's string equals the shared
  // module's is what ties this screen to the rule; asserting it CONTAINS the run
  // id, and is not the command, is what fails if someone replaces it with a
  // plausible-looking constant.
  const { state } = await entered();
  const destructive = state.destructive;
  assert.equal(destructive.phase, 'pending');
  assert.equal(destructive.readBack, readBack(DEMO_DESTRUCTIVE.verb, DEMO_DESTRUCTIVE.target));
  assert.ok(destructive.readBack.includes(DEMO_DESTRUCTIVE.target.runId));
  assert.notEqual(destructive.readBack, DEMO_DESTRUCTIVE.command);
  assert.equal(destructive.confirmed, null);
  assert.equal(destructive.refusal, null);

  // The question is the app's PROMPT QUEUE (P6.5), not a list beside it: it is
  // outstanding, it is not destructive, and its safe answer is the sample's.
  assert.equal(state.prompts.length, 1);
  assert.equal(state.prompts[0].id, DEMO_PROMPT.id);
  assert.equal(state.prompts[0].text, DEMO_PROMPT.text);
  assert.deepEqual(state.prompts[0].options, [...DEMO_PROMPT.options]);
  assert.equal(state.prompts[0].destructive, false);
  assert.equal(state.prompts[0].safe, 'no');
});

test('confirming sends the phrase as its own utterance and spends one action', async () => {
  const { demo } = await entered();
  const state = await demo.confirm();

  // The confirmation is a REAL utterance, sent after the command and minted a
  // fresh id -- which is what makes P6.2's "the utterance that issued a command
  // can never confirm it" true by construction here.
  const commandId = state.sent[2].id;
  const confirmation = state.sent[3];
  assert.equal(confirmation.text, DEMO_DESTRUCTIVE.confirmation);
  assert.equal(confirmation.ok, true);
  assert.notEqual(confirmation.id, commandId);

  const destructive = state.destructive;
  assert.equal(destructive.phase, 'idle');
  assert.equal(destructive.refusal, null);
  assert.equal(destructive.confirmed.verb, DEMO_DESTRUCTIVE.verb);
  assert.equal(destructive.confirmed.id, DEMO_DESTRUCTIVE.actionId);
  assert.equal(destructive.confirmed.confirmation.id, DEMO_DESTRUCTIVE.confirmationId);
  assert.equal(destructive.confirmed.confirmation.at, DEMO_NOW);
  assert.equal(destructive.confirmed.target.run, DEMO_DESTRUCTIVE.target.runId);
  assert.deepEqual(state.problems, []);
});

test('a second confirmation is refused: one confirmation spends one action', async () => {
  // The demo shows the machine's rule rather than hiding it behind an
  // idempotent button: the confirmation id was spent by the first tap, and the
  // second is a replay. The button stays -- the demo does not grey it out -- so
  // what a reviewer sees is the rule, not a disabled control.
  const { demo } = await entered();
  const first = await demo.confirm();
  const state = await demo.confirm();
  // The machine's own answer, whatever it is: the pending action the first tap
  // authorised is no longer outstanding, so there is nothing left to confirm.
  // (The demo asserts the code the machine gives rather than the one this test
  // would have chosen -- a demo that translated P6.2's refusals into its own
  // vocabulary would be showing its vocabulary and not the app's.)
  assert.equal(state.destructive.refusal.code, 'no-pending-action');
  assert.equal(state.destructive.refusal.field, 'actionId');
  // Both facts are kept: the action WAS authorised once, and the second attempt
  // was refused. A state that showed only the latest would tell the reviewer the
  // opposite of one of them.
  assert.equal(state.destructive.confirmed.id, first.destructive.confirmed.id);
  assert.equal(state.destructive.phase, 'idle');
  assert.equal(state.sent.length, 5, 'the refused phrase was still a sent utterance');
});

test('leaving during a confirmation is answered, not thrown at', async () => {
  // The reviewer taps Confirm and then "Leave demo mode" while the phrase is
  // still being sent. Confirming reads the demo it started with -- not the
  // module's `running`, which `leave` has just set to null -- so the leave is
  // answered with the inactive state rather than with a TypeError from a screen.
  const { demo } = await entered();
  const pending = demo.confirm();
  const left = demo.leave();
  assert.equal(left.active, false);
  const after = await pending;
  assert.equal(after.active, false);
  assert.equal(demo.active(), false);
  assert.equal(demo.state().active, false);
});

test('a confirmation in flight does not land on the demo entered after it', async () => {
  // Leave, tap "Try the demo" again, and the phrase from the FIRST demo is still
  // being sent: it must be dropped, not recorded against the new demo's
  // destructive command (which the reviewer has not confirmed).
  const { demo } = await entered();
  const pending = demo.confirm();
  demo.leave();
  const second = await demo.enter();
  await pending;

  assert.equal(second.active, true);
  assert.equal(demo.state().destructive.phase, 'pending');
  assert.equal(demo.state().destructive.confirmed, null, 'a stale phrase authorised the action');
  assert.equal(demo.state().sent.length, DEMO_UTTERANCES.length + 1, 'the stale phrase was recorded');
  assert.deepEqual(demo.state().problems, []);
});

test('entering the demo twice is the same demo, not a second one', async () => {
  // A double tap on "Try the demo" must not send the sample utterances twice,
  // and must not wipe what the reviewer has already done (the screen's control
  // and this method are the same door).
  const { demo } = await entered();
  await demo.confirm();
  const again = await demo.enter();
  assert.equal(again, demo.state());
  assert.equal(again.sent.length, 4, 'the sample utterances were not sent again');
  assert.notEqual(again.destructive.confirmed, null, 'the confirmation survived the second enter');
});

test('two overlapping enters are one demo: the double tap cannot send twice', async () => {
  // A double tap is not two calls that take turns -- both land before either has
  // finished, because `enter` awaits the client's sends in between. The guard
  // therefore cannot be "has `running` been set yet": that only happens at the
  // very end of the run, so two overlapping calls would each build their own
  // demo, send the sample utterances twice and publish twice.
  const demo = createDemoMode();
  let changes = 0;
  demo.subscribe(() => { changes += 1; });

  const first = demo.enter();
  const second = demo.enter();
  const [a, b] = await Promise.all([first, second]);

  assert.equal(changes, 1, 'the demo was entered twice');
  assert.equal(a, b, 'the two taps answered different runs');
  assert.equal(a, demo.state());
  const sentences = [...DEMO_UTTERANCES, DEMO_DESTRUCTIVE.command];
  assert.deepEqual(a.sent.map((entry) => entry.text), sentences);
  assert.equal(a.sent.length, sentences.length, 'the sample utterances were sent more than once');
  assert.deepEqual(a.problems, []);
});

test('leaving while the demo is still being built leaves it off', async () => {
  // The other half of the same hazard: `leave` is answered while the run is
  // still in flight, so the run must not land afterwards and put the banner back
  // on a screen the reviewer has already dismissed.
  const demo = createDemoMode();
  const pending = demo.enter();
  const left = demo.leave();
  assert.equal(left.active, false);
  assert.equal((await pending).active, false, 'the abandoned run landed anyway');
  assert.equal(demo.active(), false);
  assert.equal(demo.state().active, false);

  // And the next entry is a whole demo: abandoning a run leaves nothing behind.
  assert.equal((await demo.enter()).active, true);
  assert.equal(demo.state().sent.length, DEMO_UTTERANCES.length + 1);
});

test('entering again while an abandoned run is still in flight starts the demo', async () => {
  // The reviewer taps "Try the demo", taps "Leave demo mode" before it has
  // finished, and taps "Try the demo" again -- all while the first run is still
  // going. That third tap must start a demo: joining the ABANDONED run would
  // answer it with the inactive state and start nothing at all, which is a
  // control that silently does nothing.
  const demo = createDemoMode();
  let changes = 0;
  demo.subscribe(() => { changes += 1; });
  const first = demo.enter();
  demo.leave();
  const second = demo.enter();

  assert.equal((await second).active, true, 'the second tap started no demo');
  assert.equal(demo.active(), true);
  assert.equal(demo.state().active, true);
  assert.equal(demo.state().sent.length, DEMO_UTTERANCES.length + 1);

  // The abandoned run ENDING must not put the live run's place in flight back to
  // empty on its way out -- that would let the next tap start a third run, and
  // the reviewer would have two demos from two taps. (`await first` retires the
  // abandoned run; `second` is still going.)
  await first;
  const third = demo.enter();
  const [a, b] = await Promise.all([second, third]);
  assert.equal(a, b, 'the tap after the abandoned run started a second demo');
  // One publish: the live run landing. The abandoned run publishes nothing (it
  // never had a demo to show), and the two taps that joined the live run are one
  // demo, not two.
  assert.equal(changes, 1, 'the demo was entered more than once');
  assert.equal(a.active, true);
});

test('a run ending does not clear the run that replaced it', async () => {
  // The handover, made deterministic: the reviewer leaves the instant the demo
  // appears and taps "Try the demo" again, so run 2 starts while run 1 is still
  // finishing. Run 1 ending must clear only its OWN place in flight -- clearing
  // whatever is there would let the next tap start a third demo, and the reviewer
  // would have two demos from two taps.
  const demo = createDemoMode();
  let handed = false;
  let second = null;
  const stop = demo.subscribe(() => {
    if (handed) return;
    handed = true;
    demo.leave();
    second = demo.enter();
  });

  await demo.enter(); // run 1 lands, and hands over to run 2 in the listener
  assert.notEqual(second, null);
  const third = demo.enter(); // a tap while run 2 is still going
  const [a, b] = await Promise.all([second, third]);
  stop();

  assert.equal(a, b, 'the run that ended took the new run\'s place in flight with it');
  assert.equal(b.active, true);
  assert.equal(demo.active(), true);
});

test('leaving the demo drops everything, and entering again starts fresh', async () => {
  const { demo, state } = await entered();
  await demo.confirm();
  assert.notEqual(demo.state(), state);

  const left = demo.leave();
  assert.equal(demo.active(), false);
  assert.equal(left.active, false);
  assert.deepEqual(left.messages, []);
  assert.equal(left.destructive, null);
  assert.deepEqual(left.prompts, []);

  // The second run is the first run: this is what the fixed clock and the fixed
  // sample data buy, and what makes any assertion above about the demo rather
  // than about when it was entered.
  const second = await demo.enter();
  assert.deepEqual(second.sent.map((entry) => entry.id), ['demo-utterance-1', 'demo-utterance-2', 'demo-utterance-3']);
  assert.deepEqual(second, state);
});

test('confirming with no demo running does nothing at all', async () => {
  const demo = createDemoMode();
  assert.equal((await demo.confirm()).active, false);
  await demo.enter();
  demo.leave();
  assert.equal((await demo.confirm()).active, false);
});

test('subscribers hear every change, and one that throws does not stop another', async () => {
  const demo = createDemoMode();
  const heard = [];
  const stop = demo.subscribe(() => heard.push('first'));
  demo.subscribe(() => { throw new Error('a screen that fell over'); });
  demo.subscribe(() => heard.push('third'));

  await demo.enter();
  assert.deepEqual(heard, ['first', 'third']);
  await demo.confirm();
  demo.leave();
  assert.equal(heard.length, 6);

  stop();
  await demo.enter();
  assert.equal(heard.length, 7, 'only the third listener heard the last change');
  assert.throws(() => demo.subscribe('not a function'), TypeError);
});

test('the demo refuses a construction it cannot honour', () => {
  for (const options of [null, 'demo', [], 7]) {
    assert.throws(() => createDemoMode(options), TypeError, String(options));
  }
  // The clock is the demo's one input, and a demo with a clock of zero or a
  // fractional one would produce messages the app's own rules refuse.
  for (const now of [0, -1, 1.5, Number.NaN, 'now']) {
    assert.throws(() => createDemoMode({ now }), TypeError, String(now));
  }
  assert.equal(createDemoMode({ now: 1_766_000_000_001 }).state().active, false);
});

test('the words the screens use are the owner\'s, in one place', () => {
  assert.equal(DEMO_BANNER, 'Demo mode: sample data');
  assert.equal(TRY_DEMO, 'Try the demo');
  assert.equal(LEAVE_DEMO, 'Leave demo mode');
  // The banner names what the screen is: a reviewer's screenshot has to say it
  // is sample data without the reviewer having to explain.
  assert.ok(DEMO_BANNER.includes('sample data'));
});

test('the demo runs on the account the sample data names, and nothing else', async () => {
  const { state } = await entered();
  assert.deepEqual(state.account, DEMO_ACCOUNT);
  assert.deepEqual(state.installations.map((entry) => entry.label), ['Demo installation', 'Demo installation (second phone)']);
  // The jobs are sample rows and the screen says so by their names: every repo
  // in them is a demo repo, and no row carries an installation.
  for (const job of state.jobs) assert.ok(job.repo.startsWith('demo'), job.repo);
});
