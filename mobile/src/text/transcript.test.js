// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The one transcript voice and text share (mobile identity and pairing plan,
// Task X1; D2788).
//
// What a store that holds a conversation has to be true of, and what it may
// never do:
//
//   * a line keeps its place -- the order it was appended in, with the clock
//     the caller gave it and the six fields it has and no others -- so "the
//     text view shows what was said by voice" is one list read twice rather
//     than two lists that agree today;
//   * a line the store cannot read is **refused and not stored**, with its own
//     code and the field that was wrong, the way `shared/mobile-session/port.js`
//     refuses a session: a value smuggled past this check is the one thing the
//     check exists to stop;
//   * a send the client refused, and a reply the speaker could not speak, are
//     **not lines**: this store holds what reached the session, and the text
//     view's `unsent` is where refused text stays visible. Recording them here
//     would put a sentence in the conversation nobody ever heard;
//   * the conversation ends when the session does (`signedOut`), and what was
//     already said stays -- the conversation happened -- while a later line is
//     refused rather than appended to a session that is over.
//
// The session fixtures are C2's own stub (`shared/mobile-session/stub.js`),
// which is what the plan's surfaces mount on until I2 deletes it; the mailbox
// and speaker doubles are small local ones, because nothing here is about the
// client's rules -- `src/mailbox/`'s own tests are where those live.

import assert from 'node:assert/strict';
import test from 'node:test';

import { PORT_CODES, SESSION_EVENTS } from '../../../shared/mobile-session/port.js';
import { STUB_PROJECT_ID, stubSession, stubSignOut } from '../../../shared/mobile-session/stub.js';
import {
  TRANSCRIPT_CODES,
  TRANSCRIPT_SOURCE,
  TRANSCRIPT_WHO,
  createTranscript,
  isTranscript,
  recordingMailbox,
  transcriptSpeaker,
} from './index.js';

const T0 = 1_700_000_000_000;

/** A transcript over a clock and an id source a test drives. */
function makeStore(options = {}) {
  let nowMs = options.at ?? T0;
  let ids = 0;
  const transcript = createTranscript({
    session: options.session ?? null,
    now: () => nowMs,
    newId: options.newId ?? (() => `l-${++ids}`),
  });
  return {
    transcript,
    at: (value) => {
      nowMs = value;
    },
  };
}

/** One of the owner's spoken lines, shaped as `append` takes it. */
const said = (text, over = {}) => ({
  who: TRANSCRIPT_WHO.OPERATOR,
  source: TRANSCRIPT_SOURCE.VOICE,
  text,
  ...over,
});

/** A clock that is not a time, and an id source that is not an id. */
const NOON = () => 'noon';
const NO_ID = () => '';

// ---------------------------------------------------------------------------
// A line keeps its place.
// ---------------------------------------------------------------------------

test('a line carries the caller\'s clock, its own id and the six fields, in the order it was said', () => {
  const h = makeStore();
  assert.equal(h.transcript.append(said('run the tests')).ok, true);
  h.at(T0 + 5_000);
  assert.equal(
    h.transcript.append({
      who: TRANSCRIPT_WHO.AGENT,
      source: TRANSCRIPT_SOURCE.OUTBOX,
      text: 'Run 42 finished.',
    }).ok,
    true,
  );

  assert.deepEqual(h.transcript.entries(), [
    { id: 'l-1', at: T0, who: 'operator', source: 'voice', text: 'run the tests', engine: null },
    { id: 'l-2', at: T0 + 5_000, who: 'agent', source: 'outbox', text: 'Run 42 finished.', engine: null },
  ]);
  // A typed line's engine is stored as `null` rather than left off, so every
  // line has the same fields and a reader needs no second shape.
  const withEngine = makeStore();
  withEngine.transcript.append(said('say it', { engine: 'local' }));
  assert.deepEqual(Object.keys(withEngine.transcript.entries()[0]).sort(), [
    'at',
    'engine',
    'id',
    'source',
    'text',
    'who',
  ]);
  assert.equal(withEngine.transcript.entries()[0].engine, 'local');
});

test('the list handed out is a copy, and a line in it cannot be changed', () => {
  const h = makeStore();
  h.transcript.append(said('the first'));
  const handed = h.transcript.entries();
  assert.equal(Object.isFrozen(handed), true, 'the list is frozen');
  assert.equal(Object.isFrozen(handed[0]), true, 'and so is each line');
  // Pushing on what it was handed is not a way to append: a caller that could
  // would be a second writer.
  assert.throws(() => handed.push({}), TypeError);
  assert.equal(h.transcript.entries().length, 1, 'and the store did not grow');
});

// ---------------------------------------------------------------------------
// What cannot be read is refused, and is not a line.
// ---------------------------------------------------------------------------

test('a line the transcript cannot read is refused by its own code, and nothing is stored', () => {
  const cases = [
    [null, TRANSCRIPT_CODES.BAD_LINE, null],
    ['run the tests', TRANSCRIPT_CODES.BAD_LINE, null],
    [[], TRANSCRIPT_CODES.BAD_LINE, null],
    [said('x', { landed: true }), TRANSCRIPT_CODES.UNKNOWN_FIELD, 'landed'],
    [said('x', { who: 'ghost' }), TRANSCRIPT_CODES.BAD_FIELD, 'who'],
    [{ source: TRANSCRIPT_SOURCE.VOICE, text: 'x' }, TRANSCRIPT_CODES.BAD_FIELD, 'who'],
    [said('x', { source: 'somewhere' }), TRANSCRIPT_CODES.BAD_FIELD, 'source'],
    [said(''), TRANSCRIPT_CODES.BAD_FIELD, 'text'],
    [said(7), TRANSCRIPT_CODES.BAD_FIELD, 'text'],
    [said('x', { engine: 7 }), TRANSCRIPT_CODES.BAD_FIELD, 'engine'],
    [said('x', { engine: '' }), TRANSCRIPT_CODES.BAD_FIELD, 'engine'],
  ];
  for (const [line, code, field] of cases) {
    const h = makeStore();
    assert.deepEqual({ ...h.transcript.append(line) }, { ok: false, code, field }, JSON.stringify(line));
    assert.deepEqual(h.transcript.entries(), [], 'and the conversation did not take it');
  }
});

test('a line with no clock or no id to stamp it with is refused rather than stamped with a guess', () => {
  const bad = makeStore({ newId: NO_ID });
  assert.deepEqual({ ...bad.transcript.append(said('run the tests')) }, {
    ok: false,
    code: TRANSCRIPT_CODES.BAD_ID,
    field: 'id',
  });
  assert.deepEqual(bad.transcript.entries(), []);

  // The clock is the store's to read, and a clock that answers something else
  // is a wiring fault: `now` is called once, per line, and never cached.
  let calls = 0;
  const transcript = createTranscript({
    now: () => {
      calls += 1;
      return calls === 1 ? T0 : NOON();
    },
    newId: () => 'l-1',
  });
  assert.equal(transcript.append(said('the first')).ok, true);
  assert.deepEqual({ ...transcript.append(said('the second')) }, {
    ok: false,
    code: TRANSCRIPT_CODES.BAD_CLOCK,
    field: 'now',
  });
  assert.deepEqual(transcript.entries().map((one) => one.text), ['the first']);
});

test('the transcript refuses to be built without a clock and an id source', () => {
  assert.throws(() => createTranscript(), TypeError);
  assert.throws(() => createTranscript(null), TypeError);
  assert.throws(() => createTranscript({ now: T0, newId: () => 'l-1' }), TypeError);
  assert.throws(() => createTranscript({ now: () => T0 }), TypeError);
  assert.equal(isTranscript(makeStore().transcript), true);
  assert.equal(isTranscript({ append() {} }), false, 'and a shape that looks like one is not one');
  assert.equal(isTranscript(null), false);
});

// ---------------------------------------------------------------------------
// A view hears the line as it is said.
// ---------------------------------------------------------------------------

test('a subscriber hears each line as it is added, and can stop hearing', () => {
  const h = makeStore();
  const heard = [];
  const subscription = h.transcript.subscribe((line) => heard.push(line));
  assert.equal(subscription.ok, true);
  h.transcript.append(said('the first'));
  assert.deepEqual(heard.map((one) => one.text), ['the first']);
  assert.equal(Object.isFrozen(heard[0]), true, 'a subscriber is handed the frozen line');

  subscription.unsubscribe();
  h.transcript.append(said('the second'));
  assert.deepEqual(heard.map((one) => one.text), ['the first'], 'and nothing arrives after unsubscribing');
  // The store kept both: a view that stopped listening is not a conversation
  // that stopped happening.
  assert.deepEqual(h.transcript.entries().map((one) => one.text), ['the first', 'the second']);

  assert.deepEqual({ ...h.transcript.subscribe('not a listener') }, {
    ok: false,
    code: TRANSCRIPT_CODES.BAD_LISTENER,
    field: 'listener',
  });
});

test('one subscriber that throws does not stop the line reaching the others', () => {
  const h = makeStore();
  const heard = [];
  h.transcript.subscribe(() => {
    throw new Error('a view with a bug in it');
  });
  h.transcript.subscribe((line) => heard.push(line.text));

  const result = h.transcript.append(said('run the tests'));

  // The app has no logger to hand this to (the mobile rules' own sentence), so
  // the deliberate answer is to carry on: the append is the store's answer and
  // one view's fault is not the conversation's.
  assert.equal(result.ok, true);
  assert.deepEqual(heard, ['run the tests']);
  assert.deepEqual(h.transcript.entries().map((one) => one.text), ['run the tests']);
});

// ---------------------------------------------------------------------------
// The conversation belongs to a session.
// ---------------------------------------------------------------------------

test('the transcript carries the project id and ends when the session signs out', () => {
  const session = stubSession();
  const h = makeStore({ session });
  assert.equal(h.transcript.project_id, STUB_PROJECT_ID, 'the project, and never a token');
  assert.equal(h.transcript.ended(), false);
  assert.equal(h.transcript.append(said('run the tests')).ok, true);

  stubSignOut(session);

  assert.equal(h.transcript.ended(), true);
  assert.deepEqual({ ...h.transcript.append(said('and the linter')) }, {
    ok: false,
    code: TRANSCRIPT_CODES.SIGNED_OUT,
    field: 'session',
  });
  assert.deepEqual(
    h.transcript.entries().map((one) => one.text),
    ['run the tests'],
    'what was said stays: the conversation happened, even though the session is over',
  );
});

test('a transcript with no session records lines and never ends', () => {
  const h = makeStore();
  assert.equal(h.transcript.project_id, null, 'no project, because there is no session');
  assert.equal(h.transcript.ended(), false);
  assert.equal(h.transcript.append(said('run the tests')).ok, true);
});

test('a session that is not a port is refused where the app is wired, not at the first line', () => {
  // The port's own vocabulary, not a second one: what is refused is refused
  // with the code `assertSessionPort` answers with, so a caller that handles a
  // bad session handles it in one place.
  assert.throws(
    () => createTranscript({ session: 'a session', now: () => T0, newId: () => 'l-1' }),
    (error) => error.code === PORT_CODES.NOT_A_SESSION,
  );
  assert.throws(
    () => createTranscript({ session: { project_id: 'p' }, now: () => T0, newId: () => 'l-1' }),
    (error) => error.code === PORT_CODES.MISSING_MEMBER,
  );
  // The stub subscribes through the port's own event name, so a transcript that
  // listened for some other spelling would never hear the sign-out.
  const session = stubSession();
  const stream = [];
  session.on(SESSION_EVENTS.SIGNED_OUT, () => stream.push('heard'));
  stubSignOut(session);
  assert.deepEqual(stream, ['heard'], 'the event name the stub fires is the one the port names');
});

// ---------------------------------------------------------------------------
// The agent's reply: the outbox, and no other door.
// ---------------------------------------------------------------------------

/** A speaker port that records what it was asked to say, and can fail. */
function fakeSpeaker(options = {}) {
  const spoken = [];
  const verdict = Object.freeze({ ok: true, spoken: 'all of it' });
  return {
    spoken,
    async speak(text) {
      if (options.fail === true) return Object.freeze({ ok: false, code: 'speak-failed', field: 'text' });
      spoken.push(text);
      return verdict;
    },
    verdict,
  };
}

test('a reply the outbox spoke becomes the agent\'s line, and the verdict is the port\'s own', async () => {
  const h = makeStore();
  const speaker = fakeSpeaker();
  const port = transcriptSpeaker({ transcript: h.transcript, speaker });

  const answered = await port.speak('Run 42 finished.');

  assert.equal(answered, speaker.verdict, 'the client asked one question, and the answer is unchanged');
  assert.deepEqual(h.transcript.entries(), [
    {
      id: 'l-1',
      at: T0,
      who: 'agent',
      source: 'outbox',
      text: 'Run 42 finished.',
      engine: null,
    },
  ]);
});

test('a reply that was not spoken is not a line', async () => {
  const h = makeStore();
  const port = transcriptSpeaker({ transcript: h.transcript, speaker: fakeSpeaker({ fail: true }) });

  const refused = await port.speak('Run 42 finished.');

  assert.equal(refused.ok, false);
  // The client leaves the message unacked so the next poll says it again: a line
  // recorded here would be the same sentence in the conversation twice.
  assert.deepEqual(h.transcript.entries(), []);
});

test('the app\'s own refusal sentence is spoken, and is not the agent\'s line', async () => {
  // The client says its refusal through the same port it speaks replies
  // through (S6.3), so the wrapper is handed a sentence the agent never said:
  // it is what the owner is TOLD when their own words were not sent, and the
  // client marks it as what it is.
  const h = makeStore();
  const speaker = fakeSpeaker();
  const port = transcriptSpeaker({ transcript: h.transcript, speaker });
  const sentence = 'No connection. I have not sent that.';

  const said = await port.speak(sentence, { refusal: true });

  assert.equal(said, speaker.verdict, 'the client\'s question is still answered by the port itself');
  assert.deepEqual(speaker.spoken, [sentence], 'and the owner heard it');
  assert.deepEqual(h.transcript.entries(), [], 'but the agent did not say it, so it is not a line');

  // A reply spoken over the same port is unaffected -- the rule is the speech
  // the client marked, not everything that is said while the app is talking.
  await port.speak('Run 42 finished.');
  assert.deepEqual(h.transcript.entries().map((one) => [one.who, one.source, one.text]),
    [['agent', 'outbox', 'Run 42 finished.']]);
});

test('the speaker wrapper refuses a transcript or a port that is not one', () => {
  const speaker = fakeSpeaker();
  assert.throws(() => transcriptSpeaker(), TypeError);
  assert.throws(() => transcriptSpeaker({ speaker }), TypeError);
  assert.throws(() => transcriptSpeaker({ transcript: makeStore().transcript }), TypeError);
  assert.throws(() => transcriptSpeaker({ transcript: { append() {} }, speaker }), TypeError);
  assert.throws(
    () => transcriptSpeaker({ transcript: makeStore().transcript, speaker: { say: async () => ({ ok: true }) } }),
    TypeError,
  );
});

// ---------------------------------------------------------------------------
// The owner's typed words: the client's own send, watched.
// ---------------------------------------------------------------------------

/** A mailbox client as the app hands it over: a `send` and the rest of it. */
function fakeClient(options = {}) {
  const written = [];
  const verdict = Object.freeze({
    ok: true,
    code: null,
    message: Object.freeze({ id: `m-${written.length + 1}` }),
  });
  return {
    written,
    verdict,
    paths: Object.freeze({ inbox: 'mailbox/u/inbox' }),
    async send(text, sendOptions) {
      if (options.fail === true) return Object.freeze({ ok: false, code: 'offline', field: 'presence' });
      written.push({ text, options: sendOptions ?? null });
      return verdict;
    },
    unsent: () => Object.freeze([]),
  };
}

test('a typed word the client accepted is the operator\'s line, and the verdict is untouched', async () => {
  const h = makeStore();
  const client = fakeClient();
  const mailbox = recordingMailbox({ transcript: h.transcript, mailbox: client });

  const sent = await mailbox.send('ship the release notes', { verb: 'launch' });

  assert.equal(sent, client.verdict, 'the client\'s own answer, returned as it is');
  assert.deepEqual(client.written, [{ text: 'ship the release notes', options: { verb: 'launch' } }],
    'and the write is still the client\'s');
  assert.deepEqual(h.transcript.entries(), [
    {
      id: 'l-1',
      at: T0,
      who: 'operator',
      source: 'text',
      text: 'ship the release notes',
      engine: null,
    },
  ]);
});

test('a typed word the client refused is not a line, and nothing is refused of its own', async () => {
  const h = makeStore();
  const mailbox = recordingMailbox({ transcript: h.transcript, mailbox: fakeClient({ fail: true }) });

  const sent = await mailbox.send('ship the release notes', { verb: 'launch' });

  assert.deepEqual({ ...sent }, { ok: false, code: 'offline', field: 'presence' });
  // The words never reached the session: D167's `unsent` is where they stay
  // visible, and the transcription is not a history of what the owner tried.
  assert.deepEqual(h.transcript.entries(), []);
});

test('the recording mailbox is the client, with one member recorded', async () => {
  const h = makeStore();
  const client = fakeClient();
  const mailbox = recordingMailbox({ transcript: h.transcript, mailbox: client, source: TRANSCRIPT_SOURCE.VOICE });

  assert.equal(mailbox.paths, client.paths, 'the rest of the client is carried over as it was');
  assert.deepEqual(mailbox.unsent(), []);
  await mailbox.send('say it', {});
  assert.equal(h.transcript.entries()[0].source, TRANSCRIPT_SOURCE.VOICE);

  // The agent's words are the speaker wrapper's and never a send's, so the one
  // source that cannot be a send is refused where the app is wired.
  assert.throws(
    () => recordingMailbox({ transcript: h.transcript, mailbox: client, source: TRANSCRIPT_SOURCE.OUTBOX }),
    TypeError,
  );
  assert.throws(() => recordingMailbox(), TypeError);
  assert.throws(() => recordingMailbox({ mailbox: client }), TypeError);
  assert.throws(() => recordingMailbox({ transcript: h.transcript }), TypeError);
  assert.throws(
    () => recordingMailbox({ transcript: h.transcript, mailbox: { poll: async () => ({ ok: true }) } }),
    TypeError,
  );
});
