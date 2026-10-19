// The audio pipeline's tests (mobile plan, Task P3.4; D122, D123, D126, D118).
//
// This is where P3.4's verification sentence lives, verbatim: *the interfaces
// exist (AudioIn, STT, TTS) and their fakes are used by at least one test that
// exercises the voice pipeline's logic with no device and no audio: a fake
// transcript becomes a message; a fake reply becomes an utterance; an
// interrupted utterance is reported as interrupted.* The three tests below are
// those three sentences, in that order, with the fakes as the only engine
// involved -- no microphone, no synthesizer, no file, no clock of its own.
//
// The rest of the file is the rule around them, and two of those rules are
// worth naming here because they are the ones a later change is most likely to
// break silently:
//
//   * **A partial is not a message.** §5's screen shows what the user is still
//     saying, and only a `final` is composed into a message. A pipeline that
//     sent on the first partial would send half-sentences.
//   * **A `final` with no turn open is dropped.** The engine's flush races a
//     cancel or a stop, and a message built from it is a message the user asked
//     not to send (§5's grace period). The fake is written to produce exactly
//     that case so this is a test rather than a comment.

import assert from 'node:assert/strict';
import test from 'node:test';

import { validateMessage } from '../../../shared/mailbox-schema/index.js';
import { AUDIO_IN_CODES, FakeAudioIn } from './audio-in.js';
import { FakeStt } from './stt.js';
import { TTS_CODES, FakeTts } from './tts.js';
import { PIPELINE_EVENTS, PIPELINE_EVENT_FIELDS, createVoicePipeline, isVoicePipeline } from './pipeline.js';

const SENDER = 'phone-installation-a';
const CLOCK = 1_700_000_000_000;

/**
 * A pipeline over three fakes, with every event recorded. The clock and the id
 * source are counters, not a real clock and not a real random source: that is
 * the point of injecting them (see `pipeline.js`'s header).
 */
function harness({ refuse = null, stt: sttOptions } = {}) {
  const audioIn = new FakeAudioIn(refuse ? { refuse } : {});
  const stt = new FakeStt(sttOptions);
  const tts = new FakeTts();
  let ids = 0;
  const pipeline = createVoicePipeline({
    audioIn,
    stt,
    tts,
    sender: SENDER,
    newId: () => `m-${++ids}`,
    now: () => CLOCK,
  });
  const events = [];
  for (const kind of PIPELINE_EVENTS) pipeline.on(kind, (event) => events.push(event));
  return {
    audioIn,
    stt,
    tts,
    pipeline,
    events,
    /** Every event of one kind, in order. */
    of: (kind) => events.filter((event) => event.kind === kind),
    /** One whole turn, from speech to the engine's final, with no partials. */
    speak: (said) => {
      audioIn.speechStart();
      audioIn.speechEnd();
      stt.final(said);
    },
  };
}

test('the plan, sentence one: a fake transcript becomes a message', async () => {
  const h = harness();
  await h.pipeline.start();
  h.pipeline.on('message', (event) => {
    // ... and the message is the shared schema's, not a shape of this module's.
    assert.equal(validateMessage({ ...event.message, mac: '0'.repeat(64) }).ok, true);
  });

  h.audioIn.speechStart();
  h.stt.partial('run the tests');
  assert.equal(h.pipeline.turn(), true);
  h.audioIn.speechEnd();
  assert.equal(h.pipeline.turn(), true, 'speech ending is not the transcript arriving');
  h.stt.final('run the tests');

  assert.deepEqual(
    h.events.map((event) => event.kind),
    ['transcript', 'transcript', 'message'],
    'the partial, the final transcript, the message -- in that order',
  );
  assert.equal(h.pipeline.turn(), false, 'a final ends the turn');
  const [partial, final] = h.of('transcript');
  assert.deepEqual({ ...partial }, { kind: 'transcript', text: 'run the tests', final: false });
  assert.deepEqual({ ...final }, { kind: 'transcript', text: 'run the tests', final: true });
  const [message] = h.of('message');
  assert.deepEqual({ ...message.message }, { id: 'm-1', sender: SENDER, text: 'run the tests', at: CLOCK });
  assert.equal(Object.isFrozen(message.message), true, 'a message handed out is a message frozen');
  assert.deepEqual(h.of('refused'), []);
});

test('the plan, sentence two: a fake reply becomes an utterance', async () => {
  const h = harness();
  const said = h.pipeline.say('Two tests failed.');
  assert.equal(h.pipeline.speaking(), true);
  assert.deepEqual(h.of('utterance'), [], 'an utterance is reported when it ends, not when it starts');
  assert.deepEqual({ ...h.tts.finish() }, { ok: true, changed: true });
  assert.deepEqual({ ...(await said) }, { ok: true, spoken: true }, 'the port\'s verdict, verbatim');
  assert.equal(h.pipeline.speaking(), false);
  assert.deepEqual({ ...h.of('utterance')[0] }, { kind: 'utterance', state: 'finished', text: 'Two tests failed.' });
});

test('the plan, sentence three: an interrupted utterance is reported as interrupted', async () => {
  const h = harness();
  await h.pipeline.start();
  const said = h.pipeline.say('I was saying something long');
  // The barge-in: speech while the assistant is speaking. Both of the capture
  // path's two ways of saying that do it -- see the next test.
  h.audioIn.bargeIn();
  assert.deepEqual({ ...(await said) }, { ok: false, code: TTS_CODES.INTERRUPTED, field: 'text' });
  assert.equal(h.pipeline.speaking(), false);
  assert.deepEqual({ ...h.of('utterance')[0] }, { kind: 'utterance', state: 'interrupted', text: 'I was saying something long' });
  assert.equal(h.pipeline.turn(), true, 'the interruption is also the start of the turn that interrupted it');
});

test('the barge-in is a speech-start too: a simple capture path interrupts by speaking', async () => {
  // D122's fallback path may not be able to say `barge-in` at all -- that
  // signal needs echo cancellation (§5) -- so a plain `speech-start` has to
  // stop the playback as well. A pipeline that only honoured the explicit
  // signal would be one where the assistant talks over the user on exactly the
  // devices that can least afford it.
  const h = harness();
  await h.pipeline.start();
  const said = h.pipeline.say('talking over the user');
  h.audioIn.speechStart();
  assert.deepEqual({ ...(await said) }, { ok: false, code: TTS_CODES.INTERRUPTED, field: 'text' });
  assert.deepEqual({ ...h.of('utterance')[0] }, { kind: 'utterance', state: 'interrupted', text: 'talking over the user' });
  assert.equal(h.pipeline.turn(), true);
});

test('a partial is shown and is never sent', async () => {
  const h = harness();
  await h.pipeline.start();
  h.audioIn.speechStart();
  h.stt.partial('send a message to');
  assert.deepEqual(h.of('message'), [], 'half a sentence is not a message');
  h.stt.partial('send a message to mum');
  assert.deepEqual(h.of('message'), []);
  assert.equal(h.stt.turns().opened, 1, 'one turn, however many partials');
});

test('a transcript the schema refuses is refused here too, and no message is made', async () => {
  const h = harness();
  await h.pipeline.start();
  // The cough: speech that transcribes to nothing. The schema refuses it
  // (`empty-string`), and this module repairs nothing -- the code emitted is
  // the schema's own.
  h.speak('');
  assert.deepEqual({ ...h.of('refused')[0] }, { kind: 'refused', code: 'empty-string', field: 'text' });
  assert.deepEqual(h.of('message'), [], 'a refused transcript is not a message with a hole in it');
  assert.deepEqual(h.of('transcript'), []);
  assert.equal(h.pipeline.turn(), false, 'the turn is closed even when its transcript is refused');
});

test('D118/§5: a final with no turn open is dropped, before an id is even drawn', async () => {
  const h = harness();
  await h.pipeline.start();
  // No turn: the engine's flush arriving after a stop.
  h.stt.lateFinal('cancel that');
  assert.deepEqual(h.events, [], 'nothing heard, nothing said');

  // And the same after a real turn was abandoned mid-flight by `stop()`.
  h.audioIn.speechStart();
  h.stt.partial('send a message to mum');
  await h.pipeline.stop();
  h.stt.lateFinal('send a message to mum');
  assert.deepEqual(h.of('message'), [], 'the user stopped: no message');
  assert.deepEqual(
    h.of('transcript').filter((event) => event.final),
    [],
    'and not even a final transcript -- the partial that was shown before the stop stays shown',
  );
  assert.equal(h.pipeline.turn(), false);
});

test('a superseded turn\'s late final is not this turn\'s message', async () => {
  // The case a state machine alone gets **wrong** rather than merely
  // incomplete, and the reason every event carries a turn id. The user speaks,
  // stops, and starts again before the engine has flushed the first turn: the
  // old turn is abandoned, the new one is open, and the old turn's flush now
  // arrives while `state` is `open`. Keyed on the state, those words look like
  // this turn's -- so the text the user moved on from would be sent, and the
  // text they are actually saying would be swallowed, because the turn is
  // closed by the message that should never have been made.
  const h = harness();
  await h.pipeline.start();
  h.audioIn.speechStart();
  h.stt.partial('send a message to mum');
  h.audioIn.speechEnd();
  const abandoned = h.stt.turnId();
  h.audioIn.speechStart();
  const current = h.stt.turnId();
  assert.notEqual(current, abandoned, 'the new turn is a new turn');

  h.stt.lateFinal('send a message to mum'); // the abandoned turn's flush
  assert.deepEqual(h.of('message'), [], 'the cancelled text is not sent');
  assert.equal(h.pipeline.turn(), true, 'and the turn the user is in is still open');

  h.stt.partial('what I am saying now');
  h.stt.final('what I am saying now');
  assert.deepEqual(h.of('message').map((event) => event.message.text), ['what I am saying now']);
  assert.deepEqual(
    h.of('transcript').map((event) => `${event.text}:${event.final}`),
    ['send a message to mum:false', 'what I am saying now:false', 'what I am saying now:true'],
    'the abandoned turn shows nothing more, and the current turn shows exactly itself',
  );
  assert.equal(h.pipeline.turn(), false);
});

test('a partial from a turn this pipeline is not in is not shown', async () => {
  // Two ways a revision arrives out of a turn the pipeline is not in, and both
  // are the fake's `latePartial`: after the end of speech, and from the turn a
  // new speech-start abandoned. Neither is the user's current words, so
  // neither is what the screen shows.
  const h = harness();
  await h.pipeline.start();
  h.audioIn.speechStart();
  h.audioIn.speechEnd();
  const flushed = h.stt.turnId();
  assert.notEqual(flushed, 0, 'the engine still holds the flushed turn');
  h.stt.latePartial('a revision after the end of speech', flushed);
  assert.deepEqual(h.of('transcript'), [], 'the user has finished speaking: nothing is revised on screen');

  h.audioIn.speechStart();
  h.stt.latePartial('the abandoned turn speaking again');
  assert.deepEqual(h.of('transcript'), [], 'and an abandoned turn does not speak into the new one');

  h.stt.partial('exactly what I am saying');
  assert.deepEqual(h.of('transcript').map((event) => event.text), ['exactly what I am saying']);
});

test('an engine that says it is already listening is reported, and opens no turn', async () => {
  // The first of the pipeline's two defensive branches, reachable only through
  // `outOfStep` -- no honest fake can produce an engine that refuses `begin()`.
  const h = harness({ stt: { outOfStep: 'begin' } });
  await h.pipeline.start();
  h.audioIn.speechStart();
  assert.deepEqual({ ...h.of('refused')[0] }, { kind: 'refused', code: 'already-listening', field: 'turn' });
  assert.equal(h.pipeline.turn(), false, 'the mirror does not open a turn the engine refused');
  h.stt.lateFinal('the engine thinks it heard something');
  assert.deepEqual(h.of('message'), [], 'and nothing it then says becomes a message');
});

test('an engine that refuses the end of speech is reported, and its final still arrives', async () => {
  // The second branch. The mirror is left where it was rather than guessed at:
  // the turn was open, the engine says it is not listening, and the words it
  // sends afterwards are still that turn's -- so they are still shown and still
  // become a message, rather than being lost to a state the pipeline invented.
  const h = harness({ stt: { outOfStep: 'end' } });
  await h.pipeline.start();
  h.audioIn.speechStart();
  h.audioIn.speechEnd();
  assert.deepEqual({ ...h.of('refused')[0] }, { kind: 'refused', code: 'not-listening', field: 'turn' });
  assert.equal(h.pipeline.turn(), true, 'the turn is still the engine\'s, whatever it says');
  h.stt.final('the words');
  assert.deepEqual(h.of('message').map((event) => event.message.text), ['the words']);
});

test('stop() abandons the turn and interrupts an utterance in flight', async () => {
  const h = harness();
  await h.pipeline.start();
  const said = h.pipeline.say('still talking');
  h.audioIn.speechStart();
  await h.pipeline.stop();
  assert.deepEqual({ ...(await said) }, { ok: false, code: TTS_CODES.INTERRUPTED, field: 'text' });
  assert.equal(h.audioIn.running(), false);
  assert.equal(h.stt.listening(), false, 'the engine holds no turn after a stop');
  assert.equal(h.pipeline.turn(), false);
  // A stop is not a cancel of nothing: it says what it did.
  assert.deepEqual(h.of('utterance').map((event) => event.state), ['interrupted']);
});

test('a second speech-start is the same turn, not a second one', async () => {
  const h = harness();
  await h.pipeline.start();
  h.audioIn.speechStart();
  h.audioIn.speechStart();
  h.stt.partial('hello');
  assert.deepEqual(h.stt.turns(), { opened: 1, cancelled: 0 });
  assert.deepEqual(h.of('transcript').map((event) => event.text), ['hello'], 'the partials are not lost to a duplicate event');
});

test('speech starting again while the engine is flushing abandons the old transcript', async () => {
  // The capture path can start a new turn before the previous engine flush has
  // answered (the user simply kept talking after a pause). The old transcript
  // is abandoned rather than raced: if it arrived now it would arrive as the
  // new turn's.
  const h = harness();
  await h.pipeline.start();
  h.audioIn.speechStart();
  h.stt.partial('first');
  h.audioIn.speechEnd();
  assert.equal(h.stt.listening(), true, 'a flushed turn is still the engine\'s');
  h.audioIn.speechStart();
  assert.deepEqual(h.stt.turns(), { opened: 2, cancelled: 1 });
  h.stt.partial('second');
  h.stt.final('second');
  assert.deepEqual(h.of('message').map((event) => event.message.text), ['second']);
  assert.deepEqual(
    h.of('transcript').map((event) => `${event.text}:${event.final}`),
    ['first:false', 'second:false', 'second:true'],
    'the first turn\'s partial stays shown, and no revision of it arrives',
  );
});

test('each message is its own id, sender and clock, from the injected seams', async () => {
  // `newId` and `now` are the app's identity and D117's server-anchored clock;
  // this module reads neither itself, which is what keeps every test in this
  // file deterministic.
  const h = harness();
  await h.pipeline.start();
  h.speak('one');
  h.speak('two');
  assert.deepEqual(
    // `deepEqual` compares frozen objects happily; the copies are only here so
    // the expectation reads as plain data.
    h.of('message').map((event) => ({ ...event.message })),
    [
      { id: 'm-1', sender: SENDER, text: 'one', at: CLOCK },
      { id: 'm-2', sender: SENDER, text: 'two', at: CLOCK },
    ],
  );
});

test('D122: the OS refusing the microphone is a value, and an `unavailable` event', async () => {
  const denied = harness({ refuse: AUDIO_IN_CODES.MICROPHONE_DENIED });
  assert.deepEqual({ ...(await denied.pipeline.start()) }, { ok: false, code: 'microphone-denied', field: 'start' });
  assert.deepEqual({ ...denied.of('unavailable')[0] }, { kind: 'unavailable', code: 'microphone-denied' });
  assert.equal(denied.audioIn.running(), false, 'nothing was started');
  assert.deepEqual(denied.of('transcript'), [], 'and nothing was heard');

  const background = harness({ refuse: AUDIO_IN_CODES.BACKGROUND_AUDIO_DENIED });
  assert.deepEqual({ ...(await background.pipeline.start()) }, { ok: false, code: 'background-audio-denied', field: 'start' });
  assert.deepEqual({ ...background.of('unavailable')[0] }, { kind: 'unavailable', code: 'background-audio-denied' });
  // The two denials are different values because P3.5's fallback differs: one
  // is "no voice at all, tap to speak", the other is "voice works, hands-free
  // does not" (§5, D122).
  assert.notEqual(denied.of('unavailable')[0].code, background.of('unavailable')[0].code);
});

test('a refused utterance is refused, not reported as an outcome', async () => {
  const h = harness();
  const first = h.pipeline.say('the first');
  assert.deepEqual({ ...(await h.pipeline.say('the second')) }, { ok: false, code: TTS_CODES.ALREADY_SPEAKING, field: 'text' });
  assert.deepEqual({ ...(await h.pipeline.say('')) }, { ok: false, code: TTS_CODES.BAD_TEXT, field: 'text' });
  assert.deepEqual(
    h.of('refused').map((event) => event.code),
    ['already-speaking', 'bad-text'],
    'an utterance that never started is not an utterance that finished',
  );
  assert.deepEqual(h.of('utterance'), []);
  h.tts.finish();
  assert.deepEqual({ ...(await first) }, { ok: true, spoken: true });
  assert.deepEqual(h.of('utterance').map((event) => event.state), ['finished']);
});

test('D118/F26: every event carries exactly its fields, and no audio', async () => {
  // Walk a run that produces one of every kind, and assert the whole field set
  // each time. A `samples`, `frames` or `audio` field added by a later change
  // fails here rather than in review.
  const h = harness();
  await h.pipeline.start();
  h.speak('a message'); // transcript, message
  const said = h.pipeline.say('a reply');
  h.audioIn.bargeIn(); // utterance (interrupted)
  await said;
  await h.pipeline.say(''); // refused (bad-text)
  const denied = harness({ refuse: AUDIO_IN_CODES.MICROPHONE_DENIED });
  await denied.pipeline.start(); // unavailable
  h.events.push(...denied.events);

  const seen = new Set();
  for (const event of h.events) {
    seen.add(event.kind);
    assert.deepEqual(
      Object.keys(event).sort(),
      [...PIPELINE_EVENT_FIELDS[event.kind]].sort(),
      `${event.kind} carries exactly its fields`,
    );
    assert.equal(Object.isFrozen(event), true, `${event.kind} is frozen`);
  }
  assert.deepEqual([...seen].sort(), [...PIPELINE_EVENTS].sort(), 'every kind was exercised');
});

test('the pipeline is six methods, and the check says so', () => {
  const h = harness();
  assert.equal(isVoicePipeline(h.pipeline), true);
  assert.equal(isVoicePipeline({ ...h.pipeline }), true, 'the control: an unbroken copy is a pipeline');
  // ... and a real pipeline missing or breaking **one** method is not, which is
  // the case a probe of `{ start: () => {} }` cannot tell from a complete one:
  // a check weakened to three methods would still reject these two.
  assert.equal(isVoicePipeline({ ...h.pipeline, stop: undefined }), false, 'a missing method');
  assert.equal(isVoicePipeline({ ...h.pipeline, say: 'not a function' }), false, 'a method that is not one');
  for (const value of [null, undefined, 42, {}, { start: () => {} }]) {
    assert.equal(isVoicePipeline(value), false, `${JSON.stringify(value)} is not a pipeline`);
  }
});

test('a port, a sender, an id source or a clock that is missing or wrong is a programming error', () => {
  const audioIn = new FakeAudioIn();
  const stt = new FakeStt();
  const tts = new FakeTts();
  const good = { audioIn, stt, tts, sender: SENDER, newId: () => 'm-1', now: () => CLOCK };
  createVoicePipeline(good); // the control: this one is fine
  for (const [name, value] of [
    ['audioIn', undefined],
    ['audioIn', { start: () => {}, stop: () => {}, running: () => {}, on: 'x' }],
    ['stt', null],
    ['tts', { speak: () => {}, cancel: () => {} }],
    ['sender', ''],
    ['sender', 42],
    ['newId', 'm-1'],
    ['now', 42],
  ]) {
    assert.throws(() => createVoicePipeline({ ...good, [name]: value }), TypeError, `${name}: ${String(value)}`);
  }
  assert.throws(() => createVoicePipeline(), TypeError, 'no dependencies at all');
});

test('a subscriber hears its own kind, and an unsubscribe is idempotent', async () => {
  const h = harness();
  const messages = [];
  const off = h.pipeline.on('message', (event) => messages.push(event.message.text));
  await h.pipeline.start();
  h.speak('one');
  off();
  off();
  h.speak('two');
  assert.deepEqual(messages, ['one'], 'after the unsubscribe, nothing');
});

test('an unknown event and a handler that is not a function are programming errors', () => {
  const h = harness();
  // The message, not just the type: without the guard, `handlers.get(undefined)`
  // throws a TypeError too, and a test that only checked the type could not tell
  // the guard from the crash it prevents.
  assert.throws(() => h.pipeline.on('audio', () => {}), /unknown event: audio/);
  assert.throws(() => h.pipeline.on('message', 'not a function'), /a handler is a function/);
  assert.throws(() => h.pipeline.on('message', null), /a handler is a function/);
});
