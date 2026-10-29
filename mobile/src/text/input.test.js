// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The text screen's typed input (mobile plan, Task P6.9; D167, D164, D169,
// D162).
//
// The plan writes this task's verification as three claims, and this file is in
// that order:
//
//   * **typed input goes through the same mailbox client** -- and "the same"
//     is asserted against the real one: the fixture is P3.6's
//     `createMailboxClient` over the fakes that directory ships, and the
//     assertion is the record that lands in the store (one signed inbox
//     message, P6.3's fields, the client's own path), not a call count on a
//     mock. A mock would prove that this module calls *something*;
//   * **a typed destructive command goes through the same confirmation
//     machine** -- likewise P6.2's own `step`, and the assertions are its
//     outcomes and its codes: the read-back names the resolved target, the
//     typed phrase confirms once and not twice, the deadline is the machine's,
//     and a confirmation that arrives late or about a moved target is refused
//     with the machine's own code carried through unchanged;
//   * **and the one thing it does not do**: a confirmed action is **not sent as
//     text**, and here it is not sent at all. D169's rule is that a typed action
//     is an action record and not text to the agent; the record P6.2's machine
//     builds is returned, and the store stays untouched. The client's action
//     write exists (`send(text, { action })`, landed with P6.10) but nothing
//     under `src/text/` calls it -- the wiring is the screens' (P6.9's
//     remaining half, P6.12 for voice), and `input.js`'s header says why. So the
//     test asserts the store is untouched by a confirmation, and that the answer
//     says so in a named code rather than in silence. That clause is the pull
//     request's "what could not be verified" made executable.
//
// No device, no network, no Firebase project, no Expo (Global Constraint 10),
// and no clock: `now` is a variable and every id is minted by a counter.
// Everything secret-shaped below is synthetic (Global Constraint 11).

import assert from 'node:assert/strict';
import test from 'node:test';

import { CONFIRM_CUE, REFUSAL as CONFIRM_REFUSAL } from '../../../shared/confirm/index.js';
import { COMMAND, NOT_UNDERSTOOD_REPLY } from '../../../shared/command/index.js';
import { MemorySecureStore, SecretStore, secret } from '../accounts/index.js';
import { createMailboxClient } from '../mailbox/client.js';
import { MemoryConnection } from '../mailbox/connection.js';
import { macOf } from '../mailbox/mac.js';
import { MemoryMailboxStore } from '../mailbox/store.js';
import { MemorySpeaker } from '../mailbox/speaker.js';
import {
  INPUT_CODES,
  INPUT_OUTCOME,
  TYPE_CUE,
  createTypedInput,
  typedReadBack,
} from './input.js';

/** A mailbox id shaped like an Auth uid, and never a real one. */
const UID = 'AbCdEf0123456789ghIJkl';

const ACCOUNT = Object.freeze({
  id: 'laptop',
  firebaseProjectId: 'fugaro-dev',
  authUid: UID,
  fugaroProject: 'fugaro',
});

const T0 = 1_700_000_000_000;
const MINUTE = 60_000;
const INTERVAL_MS = 60_000;

/** D171's per-pairing key and D118's secret list, both synthetic. */
const MAC_KEY = 'synthetic-mac-key-0123456789abcdef';
const SECRETS = ['synthetic-token-0123456789'];

const INBOX = `mailbox/${UID}/inbox`;
/** The orchestrator's heartbeat node: D113b's third path, and never the phone's. */
const HEARTBEAT = `mailbox/${UID}/heartbeat`;
const RUN_TARGET = Object.freeze({ kind: 'run', runId: 'run-42' });
const MERGE_TARGET = Object.freeze({
  kind: 'merge',
  repo: 'fugaro/fugaro',
  pr: 7,
  headSha: 'a'.repeat(40),
});
/** The same PR a moment later: the head has moved since the read-back. */
const MOVED_TARGET = Object.freeze({
  kind: 'merge',
  repo: 'fugaro/fugaro',
  pr: 7,
  headSha: 'b'.repeat(40),
});

/**
 * A paired phone, its database, and the typed pipeline over its real client.
 *
 * `now` is a variable rather than a clock: nothing here reads the time, and a
 * test that wants "ten minutes later" says so. The id sources are counters, so
 * a failure is a stable string rather than a random one.
 */
async function harness(options = {}) {
  const db = options.db ?? new MemoryMailboxStore();
  const keys = new SecretStore(new MemorySecureStore());
  const made = secret('macKey', MAC_KEY);
  assert.equal(made.ok, true, 'the fixture key is a valid secret');
  assert.equal((await keys.put(ACCOUNT.id, 'macKey', made.secret)).ok, true);

  let nowMs = options.startMs ?? T0;
  let minted = 0;
  const client = createMailboxClient({
    account: ACCOUNT,
    keys,
    store: db,
    connection: new MemoryConnection(),
    speaker: new MemorySpeaker(),
    now: () => nowMs,
    newId: () => `m-${++minted}`,
    secrets: SECRETS,
    heartbeatIntervalMs: INTERVAL_MS,
  });

  // **The phone has looked before it types.** P6.10's gate fails closed on a
  // consumer nothing has observed (the review's M2), so the state a running
  // phone is in is one poll already made: a fresh heartbeat is delivered into
  // the database the way the orchestrator's end writes it, and the client reads
  // it once. It is fixture setup -- `deliver` is the OTHER end's write, and the
  // tests below count the client's own writes -- and a test that wants the
  // unobserved phone asks for it with `observe: false`.
  if (options.observe !== false) {
    const beat = { at: nowMs - 1_000, boot: 'boot-0001' };
    db.deliver(HEARTBEAT, {
      ...beat,
      mac: macOf(beat, MAC_KEY, 'heartbeat'),
    });
    await client.poll();
  }

  let ids = 0;
  const input = createTypedInput({
    mailbox: client,
    now: () => nowMs,
    newId: () => `x-${++ids}`,
    ...(options.router === undefined ? {} : { router: options.router }),
    ...(options.confirm === undefined ? {} : { confirm: options.confirm }),
  });
  return {
    db,
    input,
    client,
    at: (value) => {
      nowMs = value;
    },
    /** Every inbox write the client made, by id. */
    inbox: () => {
      const prefix = `${INBOX}/`;
      return db
        .writes()
        .filter((one) => one.path.startsWith(prefix))
        .map((one) => [one.path.slice(prefix.length), one.value]);
    },
  };
}

// ---------------------------------------------------------------------------
// Wiring: what is checked here rather than passed on.
// ---------------------------------------------------------------------------

test('the pipeline refuses to be built without the client, a clock and an id source', async () => {
  const h = await harness();
  const base = { mailbox: h.client, now: () => T0, newId: () => 'x-1' };
  for (const field of ['mailbox', 'now', 'newId']) {
    const broken = { ...base };
    delete broken[field];
    assert.throws(() => createTypedInput(broken), TypeError, field);
  }
  // A client without `send` is not the mailbox client this is built over.
  assert.throws(() => createTypedInput({ ...base, mailbox: {} }), TypeError);
  assert.throws(() => createTypedInput(null), TypeError);
  assert.throws(() => createTypedInput([]), TypeError);
  // A clock that is not a time is a wiring failure, not a refusal at the first
  // keystroke: the confirmation machine's state carries a watermark, so a
  // screen built without one could judge no event at all.
  for (const value of [0, -1, 1.5, 'now', null, NaN, Number.MAX_SAFE_INTEGER + 2]) {
    assert.throws(() => createTypedInput({ ...base, now: () => value }), TypeError, String(value));
  }
});

test('a router or machine option this module does not own is a wiring failure', async () => {
  const h = await harness();
  const base = { mailbox: h.client, now: () => T0, newId: () => 'x-1' };
  // P6.1's three, and nothing else: a misspelled `alias` would silently route
  // every utterance as agent text.
  assert.throws(() => createTypedInput({ ...base, router: { alias: ['Fugaro'] } }), TypeError);
  assert.throws(() => createTypedInput({ ...base, router: 'Fugaro' }), TypeError);
  // P6.2's own bounds are the machine's to enforce, and its refusal is carried
  // as the code rather than through a message that would echo the options.
  assert.throws(
    () => createTypedInput({ ...base, confirm: { timeoutMs: 1 } }),
    (error) => error instanceof TypeError && error.code === 'invalid-timeout',
  );
  assert.throws(() => createTypedInput({ ...base, confirm: { nope: 1 } }), TypeError);
});

// ---------------------------------------------------------------------------
// Agent text: the same mailbox client.
// ---------------------------------------------------------------------------

test('a typed sentence reaches the agent through the mailbox client, as one signed message', async () => {
  const h = await harness();
  const answer = await h.input.submit('ship the release notes');
  assert.equal(answer.ok, true);
  assert.equal(answer.outcome, INPUT_OUTCOME.SENT);
  assert.equal(answer.id, 'm-1', 'the id is the client\'s own');
  assert.equal(answer.at, T0, 'and so is the stamp');

  const written = h.inbox();
  assert.equal(written.length, 1, 'one utterance, one message');
  const [id, record] = written[0];
  assert.equal(id, 'm-1');
  assert.deepEqual(Object.keys(record).sort(), ['at', 'id', 'mac', 'sender', 'text']);
  assert.equal(record.text, 'ship the release notes');
  assert.equal(record.sender, UID);
  assert.equal(record.at, T0);
  assert.equal(typeof record.mac, 'string');
  assert.equal(record.mac.length > 0, true, 'the client signed it');
});

test('the free text is the transcript verbatim, and the wake word is not stripped from it', async () => {
  const h = await harness();
  // No wake word: agent text, verbatim. Present but unmatched: D164's sentence
  // and no forward at all. The pair is the point -- one is sent and one is not.
  await h.input.submit('what is the weather');
  const sent = h.inbox();
  assert.equal(sent.length, 1);
  assert.equal(sent[0][1].text, 'what is the weather');

  const missed = await h.input.submit('Fugaro, do the thing');
  assert.equal(missed.ok, true);
  assert.equal(missed.outcome, INPUT_OUTCOME.NOT_UNDERSTOOD);
  assert.equal(missed.reply, NOT_UNDERSTOOD_REPLY);
  assert.equal(h.inbox().length, 1, 'an unmatched app command is never forwarded');
});

test('a typed message the client refuses carries the client\'s own code', async () => {
  const db = new MemoryMailboxStore({ failWrites: true });
  const h = await harness({ db });
  const answer = await h.input.submit('ship it');
  assert.equal(answer.ok, false);
  assert.equal(answer.code, 'write-failed', 'P3.6\'s code, not a new one');
});

test('an utterance the router itself refuses carries the router\'s code, and is not sent', async () => {
  const h = await harness();
  // 4097 code units is past P6.1's own bound (P0.2's TEXT_MAX, which is also
  // what a forwarded message can carry), so the router refuses it rather than
  // truncating: the code is the router's, carried unchanged.
  const answer = await h.input.submit('a'.repeat(4097));
  assert.equal(answer.ok, false);
  assert.equal(answer.code, 'utterance-too-long');
  assert.equal(h.inbox().length, 0);
  // And a control character is the other refusal a typed line can carry in.
  const control = await h.input.submit('ship it\u0007');
  assert.equal(control.ok, false);
  assert.equal(control.code, 'utterance-control-character');
  assert.equal(h.inbox().length, 0);
});

test('an empty or non-text utterance is refused before anything is routed or sent', async () => {
  const h = await harness();
  for (const value of ['', null, undefined, 42, {}, ['a']]) {
    const answer = await h.input.submit(value);
    assert.equal(answer.ok, false, String(value));
    assert.equal(answer.code, INPUT_CODES.BAD_TEXT);
  }
  assert.equal(h.inbox().length, 0);
});

test('a clock that stops being a time is a refusal, not a crash', async () => {
  const h = await harness();
  h.at(NaN);
  const answer = await h.input.submit('ship it');
  assert.equal(answer.ok, false);
  assert.equal(answer.code, INPUT_CODES.BAD_CLOCK);
  assert.equal(h.inbox().length, 0, 'nothing was stamped with a non-time');
  // And it is refused HERE rather than by whoever would have stamped it: a
  // wake-word utterance never reaches the client, so a screen without its own
  // clock check would answer "what's pending" from a clock that is not a time.
  const pending = await h.input.submit("Fugaro, what's pending");
  assert.equal(pending.ok, false);
  assert.equal(pending.code, INPUT_CODES.BAD_CLOCK);
  assert.equal((await h.input.awaiting()).code, INPUT_CODES.BAD_CLOCK);
  assert.equal((await h.input.propose({ text: 'x', verb: 'launch' })).code, INPUT_CODES.BAD_CLOCK);
});

test('a wake word P6.2\'s machine does not recognise beside "confirm" is a wiring failure', async () => {
  const h = await harness();
  const base = { mailbox: h.client, now: () => T0, newId: () => 'x-1' };
  // `isConfirmPhrase` routes with the router's DEFAULT aliases, so a screen
  // whose wake word is not one of them could route "Hey, confirm" as the
  // command and have the machine refuse it `not-a-confirmation`: every command
  // but the one that authorises an action. Fail-closed, and refused where the
  // app is wired rather than discovered mid-action.
  assert.throws(
    () => createTypedInput({ ...base, router: { aliases: ['Hey'] } }),
    /no configured wake word can confirm/,
  );
  assert.throws(
    () => createTypedInput({ ...base, router: { aliases: [] } }),
    /no configured wake word can confirm/,
  );
  assert.throws(
    () => createTypedInput({ ...base, router: { aliases: 'Fugaro' } }),
    /no configured wake word can confirm/,
  );
  // The machine's own wake words are the ones that work, singly or beside a
  // configured one, and "Figaro, confirm" is P6.2's own example of the phrase.
  for (const aliases of [['Fugaro'], ['fugaro'], ['Figaro'], ['Hey', 'Fugaro']]) {
    const built = createTypedInput({ ...base, router: { aliases } });
    assert.equal(built.ok, true, aliases.join('/'));
  }
  let minted = 0;
  const figaro = createTypedInput({
    mailbox: h.client,
    now: () => T0,
    newId: () => `f-${++minted}`,
    router: { aliases: ['Figaro'] },
  });
  await figaro.propose({ text: 'cancel run 42', verb: 'cancel', target: RUN_TARGET });
  const answer = await figaro.submit('Figaro, confirm');
  assert.equal(answer.outcome, INPUT_OUTCOME.CONFIRMED, 'the configured wake word confirms');
});

test('a context field this module does not read is refused, not ignored', async () => {
  const h = await harness();
  // A misspelled `target` on a confirmation would be a confirmation against
  // whatever the module happened to default to.
  const answer = await h.input.submit('Fugaro, confirm', { targt: RUN_TARGET });
  assert.equal(answer.ok, false);
  assert.equal(answer.code, INPUT_CODES.BAD_TEXT);
  assert.equal(h.inbox().length, 0);
});

// ---------------------------------------------------------------------------
// The rest of P6.1's routes: this screen answers two, and hands on the others.
// ---------------------------------------------------------------------------

test('"what\'s pending" is this screen\'s question, and it sends nothing', async () => {
  const h = await harness({ router: { aliases: ['Fugaro'] } });
  const answer = await h.input.submit("Fugaro, what's pending");
  assert.equal(answer.ok, true);
  assert.equal(answer.outcome, INPUT_OUTCOME.PENDING);
  assert.equal(h.inbox().length, 0);
});

test('a command that belongs to another screen is named as such rather than answered here', async () => {
  const h = await harness({
    router: { aliases: ['Fugaro'], projects: [{ id: 'fugaro', spokenName: 'Fugaro' }] },
  });
  const answer = await h.input.submit('Fugaro, switch to Fugaro');
  assert.equal(answer.ok, true);
  assert.equal(answer.outcome, INPUT_OUTCOME.ELSEWHERE);
  assert.equal(answer.command, COMMAND.SWITCH_PROJECT);
  assert.equal(h.inbox().length, 0, 'the switcher\'s command is not agent text');
});

// ---------------------------------------------------------------------------
// The confirmation machine: a read-back, the typed phrase, and the record.
// ---------------------------------------------------------------------------

test('a resolved destructive command is read back in the machine\'s words and names the resolved target', async () => {
  const h = await harness();
  const proposed = await h.input.propose({ text: 'cancel run 42', verb: 'cancel', target: RUN_TARGET });
  assert.equal(proposed.ok, true);
  assert.equal(proposed.outcome, INPUT_OUTCOME.READ_BACK);
  assert.equal(proposed.actionId, 'x-1');
  assert.equal(proposed.deadline, T0 + MINUTE, 'P6.2\'s default timeout, not this module\'s');
  assert.equal(proposed.readBack.includes('run-42'), true, 'the read-back names the resolved run');
  assert.equal(proposed.readBack.endsWith(TYPE_CUE), true, 'and it asks for the typed phrase');
  assert.equal(
    proposed.readBack.includes('Say "Fugaro, confirm"'),
    false,
    'the spoken cue must not reach a screen with a keyboard',
  );
  assert.equal(h.inbox().length, 0, 'a read-back is not a send');
});

test('a bare "confirm" is agent text: the phrase must carry the wake word to be routed', async () => {
  const h = await harness();
  // D164's half of D169: there is deliberately no string-level predicate for a
  // confirmation, so the bare word cannot confirm anything -- it is a message
  // for the agent, and the agent answers it.
  const bare = await h.input.submit('confirm');
  assert.equal(bare.outcome, INPUT_OUTCOME.SENT);
  assert.equal(h.inbox().length, 1);
  assert.deepEqual((await h.input.awaiting()).actions, [], 'and it authorised nothing');
});

test('the typed phrase confirms once, produces P6.3\'s action record, and sends nothing', async () => {
  const h = await harness();
  const proposed = await h.input.propose({ text: 'cancel run 42', verb: 'cancel', target: RUN_TARGET });

  const answer = await h.input.submit('Fugaro, confirm', { actionId: proposed.actionId });
  assert.equal(answer.ok, true);
  assert.equal(answer.outcome, INPUT_OUTCOME.CONFIRMED);
  assert.equal(answer.verb, 'cancel');
  assert.equal(answer.actionId, proposed.actionId);
  assert.equal(answer.confirmedAt, T0);

  // P6.3's record, field for field: the id, the verb, the target, the
  // confirmation. The target is the one READ BACK, never one asserted now.
  assert.deepEqual(answer.record, {
    id: proposed.actionId,
    verb: 'cancel',
    target: { run: 'run-42' },
    confirmation: { id: answer.confirmationId, at: T0 },
  });

  // **And it is not sent.** The client CAN carry this record -- `send(text,
  // { action })` landed with P6.10 and is what P6.10's "an action record is
  // never queued" is about -- but nothing in `src/text/` calls it: the wiring
  // is the screens' (P6.9's remaining half, P6.12 for the voice path) and
  // `input.js`'s header says so. So the record is returned, with the reason it
  // is not in the database, and that reason is still true.
  assert.equal(answer.delivered, false);
  assert.equal(answer.code, INPUT_CODES.NO_ACTION_WRITE);
  assert.equal(h.inbox().length, 0, 'the confirmation wrote nothing at all');
});

test('a confirmation authorises exactly one action: the phrase spent, a second is refused', async () => {
  const h = await harness();
  const proposed = await h.input.propose({ text: 'cancel run 42', verb: 'cancel', target: RUN_TARGET });
  assert.equal((await h.input.submit('Fugaro, confirm', { actionId: proposed.actionId })).ok, true);
  const again = await h.input.submit('Fugaro, confirm', { actionId: proposed.actionId });
  assert.equal(again.ok, false);
  assert.equal(again.code, CONFIRM_REFUSAL.NO_PENDING_ACTION, 'P6.2\'s code, carried unchanged');
});

test('the phrase with nothing outstanding is the machine\'s refusal, not a silent success', async () => {
  const h = await harness();
  const answer = await h.input.submit('Fugaro, confirm');
  assert.equal(answer.ok, false);
  assert.equal(answer.code, CONFIRM_REFUSAL.NO_PENDING_ACTION);
  assert.equal(h.inbox().length, 0);
});

test('a confirmation after the machine\'s deadline is refused with the machine\'s own code', async () => {
  const h = await harness({ confirm: { timeoutMs: 60_000 } });
  const proposed = await h.input.propose({ text: 'cancel run 42', verb: 'cancel', target: RUN_TARGET });
  h.at(T0 + 2 * MINUTE);
  // The caller is holding the read-back it drew past its deadline, so it names
  // the action and re-states the target; the machine then says WHY.
  const answer = await h.input.submit('Fugaro, confirm', {
    actionId: proposed.actionId,
    target: RUN_TARGET,
  });
  assert.equal(answer.ok, false);
  assert.equal(answer.code, CONFIRM_REFUSAL.CONFIRMATION_EXPIRED);
  assert.equal(h.inbox().length, 0);
});

test('a target that moved since the read-back is refused rather than merged against it', async () => {
  const h = await harness();
  const proposed = await h.input.propose({ text: 'merge PR 7', verb: 'merge', target: MERGE_TARGET });
  const answer = await h.input.submit('Fugaro, confirm', {
    actionId: proposed.actionId,
    target: MOVED_TARGET,
  });
  assert.equal(answer.ok, false);
  assert.equal(answer.code, CONFIRM_REFUSAL.DIFFERENT_TARGET);
  assert.equal(h.inbox().length, 0, 'a moved PR was not merged');
});

test('a confirmation re-stating the same target is authorised, and the record binds the head read back', async () => {
  const h = await harness();
  const proposed = await h.input.propose({ text: 'merge PR 7', verb: 'merge', target: MERGE_TARGET });
  const answer = await h.input.submit('Fugaro, confirm', {
    actionId: proposed.actionId,
    target: MERGE_TARGET,
  });
  assert.equal(answer.ok, true);
  assert.equal(answer.outcome, INPUT_OUTCOME.CONFIRMED);
  assert.deepEqual(answer.record.target, { repo: 'fugaro/fugaro', pr: 7 });
  assert.equal(answer.record.head_sha, MERGE_TARGET.headSha, 'the head the read-back named');
});

test('"cancel that" drops the read-backs, and says so when there were none', async () => {
  const h = await harness();
  const empty = await h.input.submit('Fugaro, cancel that');
  assert.equal(empty.ok, true);
  assert.equal(empty.outcome, INPUT_OUTCOME.NOTHING_PENDING);

  const proposed = await h.input.propose({ text: 'cancel run 42', verb: 'cancel', target: RUN_TARGET });
  const dropped = await h.input.submit('Fugaro, cancel that');
  assert.equal(dropped.ok, true);
  assert.equal(dropped.outcome, INPUT_OUTCOME.DISCARDED);
  assert.deepEqual(dropped.actionIds, [proposed.actionId]);

  // Dropped means gone: the phrase now has nothing to authorise.
  const answer = await h.input.submit('Fugaro, confirm');
  assert.equal(answer.code, CONFIRM_REFUSAL.NO_PENDING_ACTION);
});

test('two read-backs outstanding are the machine\'s ambiguity, not a guess between them', async () => {
  const h = await harness();
  const cancel = await h.input.propose({ text: 'cancel run 42', verb: 'cancel', target: RUN_TARGET });
  await h.input.propose({ text: 'merge PR 7', verb: 'merge', target: MERGE_TARGET });
  assert.equal((await h.input.awaiting()).actions.length, 2);
  const answer = await h.input.submit('Fugaro, confirm', { actionId: cancel.actionId, target: RUN_TARGET });
  assert.equal(answer.ok, false);
  assert.equal(answer.code, 'ambiguous-pending', 'P6.2\'s code: nothing is authorised');
  assert.equal(h.inbox().length, 0);
});

test('the phrase with no actionId confirms the one live read-back the screen is drawing', async () => {
  // The flow the screen actually implies: `awaiting()` (or `propose`'s own
  // answer) gives the app the read-back it draws, and the owner then types the
  // phrase. The app need not hold the id to hand it back -- one read-back
  // waiting has exactly one thing the phrase could mean -- so the id and the
  // target come from the machine's own listing.
  const h = await harness();
  const proposed = await h.input.propose({ text: 'cancel run 42', verb: 'cancel', target: RUN_TARGET });
  const live = await h.input.awaiting();
  assert.equal(live.actions.length, 1);
  assert.equal(live.actions[0].actionId, proposed.actionId);

  const answer = await h.input.submit('Fugaro, confirm');
  assert.equal(answer.ok, true);
  assert.equal(answer.outcome, INPUT_OUTCOME.CONFIRMED);
  assert.equal(answer.actionId, proposed.actionId, 'the id is the live read-back\'s own');
  assert.deepEqual(answer.record.target, { run: 'run-42' }, 'and so is the target it re-states');
  assert.equal(answer.delivered, false);
  assert.equal(h.inbox().length, 0);
  // Spent: a second bare phrase has nothing left to authorise.
  assert.equal((await h.input.submit('Fugaro, confirm')).code, CONFIRM_REFUSAL.NO_PENDING_ACTION);
});

test('awaiting() reports what the typed phrase would confirm, in the machine\'s words', async () => {
  const h = await harness();
  assert.deepEqual((await h.input.awaiting()).actions, []);
  await h.input.propose({ text: 'cancel run 42', verb: 'cancel', target: RUN_TARGET });
  const live = await h.input.awaiting();
  assert.equal(live.ok, true);
  assert.equal(live.actions.length, 1);
  assert.equal(live.actions[0].actionId, 'x-1');
  assert.equal(live.actions[0].readBack.includes('run-42'), true);
  assert.equal(
    live.actions[0].readBack.endsWith(TYPE_CUE),
    true,
    'what the screen draws is what the screen can take: the typed phrase',
  );
  assert.equal(live.actions[0].readBack.includes('Say "'), false);
  // And one that has timed out is not offered any more.
  h.at(T0 + 2 * MINUTE);
  assert.deepEqual((await h.input.awaiting()).actions, []);
});

// ---------------------------------------------------------------------------
// D162: a launch passes through, through the same client.
// ---------------------------------------------------------------------------

test('a launch passes straight through to the agent, through the mailbox client', async () => {
  const h = await harness();
  const answer = await h.input.propose({ text: 'kick off the nightly build', verb: 'launch' });
  assert.equal(answer.ok, true);
  assert.equal(answer.outcome, INPUT_OUTCOME.SENT, 'no read-back, D162');
  assert.equal(h.inbox().length, 1);
  assert.equal(h.inbox()[0][1].text, 'kick off the nightly build');
  // And nothing is left waiting for a confirmation.
  assert.deepEqual((await h.input.awaiting()).actions, []);
});

// ---------------------------------------------------------------------------
// propose's own inputs.
// ---------------------------------------------------------------------------

test('propose refuses a field it does not read rather than ignoring it', async () => {
  const h = await harness();
  // A misspelled `target` would read back the wrong thing, so it is refused.
  const answer = await h.input.propose({ text: 'cancel run 42', verb: 'cancel', targt: RUN_TARGET });
  assert.equal(answer.ok, false);
  assert.equal(answer.code, INPUT_CODES.BAD_TEXT);
  assert.equal((await h.input.propose({ verb: 'cancel', target: RUN_TARGET })).ok, false);
  assert.equal((await h.input.propose(null)).ok, false);
  assert.equal(h.inbox().length, 0);
});

test('a verb the machine does not know is the machine\'s refusal, carried unchanged', async () => {
  const h = await harness();
  // A verb P6.2 refuses outright by the 2026-10-17 ruling (the kill switch, the
  // deletes): refused whatever the target says.
  const refused = await h.input.propose({ text: 'delete everything', verb: 'delete', target: RUN_TARGET });
  assert.equal(refused.ok, false);
  assert.equal(refused.code, 'refused-verb', 'P6.2\'s code');
  // And a verb nobody has heard of is a different refusal, not the same one.
  const unknown = await h.input.propose({ text: 'frobnicate it', verb: 'frobnicate', target: RUN_TARGET });
  assert.equal(unknown.ok, false);
  assert.equal(unknown.code, 'unknown-verb', 'P6.2\'s code');
  assert.equal(h.inbox().length, 0);
});

test('a destructive verb with no target is refused by the machine, not defaulted to something', async () => {
  const h = await harness();
  const answer = await h.input.propose({ text: 'cancel it', verb: 'cancel' });
  assert.equal(answer.ok, false);
  assert.equal(answer.code, 'unresolved-target', 'the machine will not read back nothing');
  assert.equal(h.inbox().length, 0);
});

// ---------------------------------------------------------------------------
// The two cues, and that they are the same string.
// ---------------------------------------------------------------------------

test('the typed cue is the fixed phrase, and it is not P6.2\'s spoken one', () => {
  assert.equal(TYPE_CUE.includes('Fugaro, confirm'), true);
  assert.equal(TYPE_CUE.includes('Type'), true);
  assert.notEqual(TYPE_CUE, CONFIRM_CUE, 'P6.2\'s cue says "Say": a keyboard does not');
});

test('typedReadBack swaps the instruction and leaves the sentence that names the target alone', () => {
  // D47's Rule: the read-back names the resolved target, never the transcript.
  // The swap touches the instruction and nothing else, which is why the
  // sentence before it survives character for character.
  const spoken = `Cancel run ${RUN_TARGET.runId}. ${CONFIRM_CUE}`;
  const typed = typedReadBack(spoken);
  assert.equal(typed.endsWith(TYPE_CUE), true);
  assert.equal(typed.startsWith('Cancel run run-42.'), true, 'the target survives the swap');
  assert.equal(typed.includes('Say'), false);
  assert.equal(spoken.includes('Say'), true, 'and the input really was P6.2\'s spoken one');
  // One sentence, one instruction -- never a "Type" appended after a "Say".
  assert.equal(typed.split('Fugaro, confirm').length - 1, 1);
});

test('typedReadBack edits only what it recognises, and returns everything else untouched', () => {
  // A sentence this module does not recognise is not one it will edit: the only
  // safe edit is the one that provably touches nothing but the instruction.
  const foreign = 'Do the thing. Ask your assistant to proceed.';
  assert.equal(typedReadBack(foreign), foreign);
  // A read-back with no sentence at all is the cue, not a leading space.
  assert.equal(typedReadBack(CONFIRM_CUE), TYPE_CUE);
  // And a value that is not a string is passed through rather than coerced.
  for (const value of [null, undefined, 42, {}, ['a']]) {
    assert.equal(typedReadBack(value), value, String(value));
  }
});
