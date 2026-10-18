// The transcription port's tests (mobile plan, Task P3.4; D123, D118, D126).
//
// What is asserted is the turn's contract: one turn at a time, partials while
// the user speaks, one final per turn, and a cancelled turn that produces no
// final. The last one is the rule §5's "cancel that" grace period rests on,
// and the fake is written so the case can be *tested* rather than assumed: it
// can deliver a final with no turn open (`lateFinal`), and the pipeline must
// not turn that into a message.

import assert from 'node:assert/strict';
import test from 'node:test';

import { STT_CODES, STT_EVENTS, STT_EVENT_FIELDS, FakeStt, isSpeechToText } from './stt.js';

test('the port is five methods, and the check says so', () => {
  assert.equal(isSpeechToText(new FakeStt()), true);
  for (const value of [null, undefined, [], {}, { begin: () => {}, end: () => {}, cancel: () => {}, listening: () => {}, on: 'x' }]) {
    assert.equal(isSpeechToText(value), false, `${JSON.stringify(value)} is not a port`);
  }
});

test('one turn at a time: begin, end, and the refusals between them', () => {
  const stt = new FakeStt();
  assert.equal(stt.listening(), false);
  assert.deepEqual({ ...stt.end() }, { ok: false, code: STT_CODES.NOT_LISTENING, field: 'turn' });
  assert.deepEqual({ ...stt.begin() }, { ok: true, changed: true });
  assert.equal(stt.listening(), true);
  assert.deepEqual({ ...stt.begin() }, { ok: false, code: STT_CODES.ALREADY_LISTENING, field: 'turn' });
  assert.deepEqual({ ...stt.end() }, { ok: true, changed: true });
  assert.equal(stt.listening(), true, 'a flushed turn is still the engine\'s -- see the next test');
  stt.final('hello');
  assert.equal(stt.listening(), false);
  assert.deepEqual({ ...stt.turns() }, { opened: 1, cancelled: 0 });
});

test('partials arrive while the user speaks, and only then', () => {
  const stt = new FakeStt();
  const heard = [];
  stt.on('partial', (event) => heard.push(event.text));
  assert.deepEqual({ ...stt.partial('hel') }, { ok: false, code: STT_CODES.NOT_LISTENING, field: 'turn' });
  stt.begin();
  stt.partial('hel');
  stt.partial('hello');
  assert.deepEqual(heard, ['hel', 'hello']);
  stt.final('hello there');
  assert.equal(stt.listening(), false, 'a final closes the turn');
  assert.deepEqual(
    stt.emitted().map((event) => event.kind),
    ['partial', 'partial', 'final'],
  );
});

test('the end of speech flushes the turn; the final is what ends it', () => {
  // The distinction the pipeline's turn state rests on: a `final` arrives
  // *after* the capture path says the user stopped, so `end()` must not close
  // the turn -- an `end()` that did would leave the engine's answer with
  // nowhere to land, and every message would be dropped.
  const stt = new FakeStt();
  stt.begin();
  assert.deepEqual({ ...stt.end() }, { ok: true, changed: true });
  assert.equal(stt.listening(), true, 'the engine still holds the turn');
  assert.deepEqual({ ...stt.end() }, { ok: true, changed: false }, 'a second end of speech is not an error');
  assert.deepEqual({ ...stt.partial('no more revisions') }, { ok: false, code: STT_CODES.NOT_LISTENING, field: 'turn' });
  assert.equal(stt.final('hello there').ok, true, 'the final is what ends the turn');
  assert.equal(stt.listening(), false);
  // ... and a turn cancelled while flushing is abandoned, not flushed.
  stt.begin();
  stt.end();
  assert.deepEqual({ ...stt.cancel() }, { ok: true, changed: true });
  assert.equal(stt.listening(), false);
  assert.deepEqual({ ...stt.final('too late') }, { ok: false, code: STT_CODES.NOT_LISTENING, field: 'turn' });
});

test('D123: a partial is a fact the interface carries, and text is all it carries', () => {
  assert.deepEqual([...STT_EVENTS], ['partial', 'final']);
  assert.deepEqual([...STT_EVENT_FIELDS], ['kind', 'text']);
  const stt = new FakeStt();
  stt.begin();
  stt.partial('he');
  for (const event of stt.emitted()) {
    assert.deepEqual(Object.keys(event), [...STT_EVENT_FIELDS]);
    assert.equal(Object.isFrozen(event), true);
  }
  assert.throws(() => stt.partial(42), TypeError, 'a transcript is a string');
  assert.throws(() => stt.final(null), TypeError);
});

test('a cancelled turn is abandoned, and cancelling twice is not an error', () => {
  const stt = new FakeStt();
  stt.begin();
  assert.deepEqual({ ...stt.cancel() }, { ok: true, changed: true });
  assert.deepEqual({ ...stt.cancel() }, { ok: true, changed: false });
  assert.equal(stt.listening(), false);
  assert.deepEqual({ ...stt.turns() }, { opened: 1, cancelled: 1 });
  // The engine is not asked for a final for a turn nobody is in any more.
  assert.deepEqual({ ...stt.final('hello') }, { ok: false, code: STT_CODES.NOT_LISTENING, field: 'turn' });
});

test('the engine can flush a final with no turn open, and the port does not pretend otherwise', () => {
  // This is the case a real engine produces -- its flush racing a cancel or a
  // stop -- and the only reason the fake can produce it is that the pipeline's
  // protection against it has to be testable. The port promises no such
  // final; the fake says the word `late` so a reader can see which of the two
  // it is.
  const stt = new FakeStt();
  const finals = [];
  stt.on('final', (event) => finals.push(event.text));
  assert.equal(stt.lateFinal('cancel that').ok, true);
  assert.deepEqual(finals, ['cancel that'], 'the fake delivers it, which is the point');
  assert.equal(stt.listening(), false);
  assert.deepEqual({ ...stt.turns() }, { opened: 0, cancelled: 0 });
});

test('a subscriber hears its own kind, and an unsubscribe is idempotent', () => {
  const stt = new FakeStt();
  const finals = [];
  const off = stt.on('final', (event) => finals.push(event.text));
  stt.begin();
  stt.final('one');
  off();
  off();
  stt.begin();
  stt.final('two');
  assert.deepEqual(finals, ['one']);
});

test('an unknown event and a handler that is not a function are programming errors', () => {
  const stt = new FakeStt();
  assert.throws(() => stt.on('transcript', () => {}), TypeError);
  assert.throws(() => stt.on('final', {}), TypeError);
});
