// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The speaker's double (P3.6; P3.4's text-to-speech port is the real seam, and
// P3.5's `src/voice/speaker.js` is the adapter that hands a built app's client
// one -- this module still defines the port and the double its own tests use).
//
// Two things are asserted, and both are used by the client's tests: a speak
// records the text, and a speak can fail without recording anything. The
// second is what makes "it acks a message only after it has spoken it" a test
// about the ack and not about the speaker.

import assert from 'node:assert/strict';
import test from 'node:test';

import { MemorySpeaker, SPEAK_CODES } from './speaker.js';

test('a speak records the text, in order', async () => {
  const speaker = new MemorySpeaker();
  assert.deepEqual(await speaker.speak('run 42 finished'), { ok: true });
  assert.deepEqual(await speaker.speak('merge it'), { ok: true });
  assert.deepEqual(speaker.spoken(), ['run 42 finished', 'merge it']);
});

test('there is nothing to speak in an empty string, and nothing is recorded', async () => {
  const speaker = new MemorySpeaker();
  for (const text of ['', undefined, null, 42, {}, ['a']]) {
    const refused = await speaker.speak(text);
    assert.equal(refused.ok, false, String(text));
    assert.equal(refused.code, SPEAK_CODES.BAD_TEXT, String(text));
  }
  assert.deepEqual(speaker.spoken(), []);
});

test('a failed speak records nothing, and stops failing when it is told to', async () => {
  // An interrupted utterance, an unrouted device: the text was NOT spoken, and
  // a speaker that recorded it anyway would make the ack's rule untestable.
  const speaker = new MemorySpeaker({ fail: true });
  const refused = await speaker.speak('merge it');
  assert.equal(refused.code, SPEAK_CODES.SPEAK_FAILED);
  assert.deepEqual(speaker.spoken(), []);
  speaker.setFailing(false);
  assert.deepEqual(await speaker.speak('merge it'), { ok: true });
  assert.deepEqual(speaker.spoken(), ['merge it']);
});
