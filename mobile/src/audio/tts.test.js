// The speech output port's tests (mobile plan, Task P3.4; D127, D118, D126).
//
// The utterance is the unit and its outcome is a value: played to the end, or
// interrupted. The fake has no timer in it -- the test decides when an
// utterance ends -- which is what lets a barge-in be asserted at the exact
// moment it is decided (D52's CI half of criterion 5; the ~300 ms is
// owner-run check 86).

import assert from 'node:assert/strict';
import test from 'node:test';

import { FakeTts, TTS_CODES, isTextToSpeech } from './tts.js';

test('the port is three methods, and the check says so', () => {
  assert.equal(isTextToSpeech(new FakeTts()), true);
  for (const value of [null, undefined, {}, { speak: () => {}, cancel: () => {}, speaking: 'x' }]) {
    assert.equal(isTextToSpeech(value), false, `${JSON.stringify(value)} is not a port`);
  }
});

test('an utterance plays to the end, and says it did', async () => {
  const tts = new FakeTts();
  const said = tts.speak('Done: two tests failed.');
  assert.equal(tts.speaking(), true, 'the utterance is in flight until it ends');
  assert.deepEqual({ ...tts.finish() }, { ok: true, changed: true });
  assert.deepEqual({ ...(await said) }, { ok: true, spoken: true });
  assert.equal(tts.speaking(), false);
  assert.deepEqual([...tts.spoken()], ['Done: two tests failed.']);
  assert.deepEqual([...tts.interrupted()], []);
});

test('an interrupted utterance is reported as interrupted', async () => {
  const tts = new FakeTts();
  const said = tts.speak('I was saying something long');
  assert.deepEqual({ ...tts.cancel() }, { ok: true, changed: true });
  assert.deepEqual({ ...(await said) }, { ok: false, code: TTS_CODES.INTERRUPTED, field: 'text' });
  assert.equal(tts.speaking(), false);
  assert.deepEqual([...tts.interrupted()], ['I was saying something long']);
  assert.equal(tts.cancels(), 1);
});

test('cancelling with nothing playing is a no-op, and finishes nothing', () => {
  const tts = new FakeTts();
  assert.deepEqual({ ...tts.cancel() }, { ok: true, changed: false });
  assert.equal(tts.cancels(), 0, 'a cancel that stopped nothing is not a cancel');
  assert.deepEqual(
    { ...tts.finish() },
    { ok: false, code: TTS_CODES.NOT_SPEAKING, field: 'utterance' },
  );
});

test('there has to be something to say', async () => {
  const tts = new FakeTts();
  for (const text of ['', 42, null, undefined]) {
    const verdict = await tts.speak(text);
    assert.deepEqual({ ...verdict }, { ok: false, code: TTS_CODES.BAD_TEXT, field: 'text' }, `${String(text)}`);
  }
  assert.deepEqual([...tts.spoken()], [], 'nothing was handed to an engine');
  assert.equal(tts.speaking(), false);
});

test('one utterance at a time: a second is refused, not queued, and the first is untouched', async () => {
  const tts = new FakeTts();
  const first = tts.speak('the first');
  const second = await tts.speak('the second');
  assert.deepEqual({ ...second }, { ok: false, code: TTS_CODES.ALREADY_SPEAKING, field: 'text' });
  assert.equal(tts.speaking(), true, 'the first utterance is still the one playing');
  assert.deepEqual([...tts.spoken()], ['the first'], 'the refused text never reached the engine');
  tts.finish();
  assert.deepEqual({ ...(await first) }, { ok: true, spoken: true });
});

test('the port hands back no audio, and stores none (D118)', async () => {
  const tts = new FakeTts();
  const said = tts.speak('text in, an outcome out');
  tts.finish();
  const verdict = await said;
  assert.deepEqual(Object.keys(verdict).sort(), ['ok', 'spoken'], 'the outcome carries no samples');
  assert.deepEqual([...tts.spoken()], ['text in, an outcome out'], 'what is kept is the text');
});
