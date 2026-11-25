// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// What arrives through the share target, read into the U7 answers (task M0;
// Check 733's U7).

import assert from 'node:assert/strict';
import test from 'node:test';

import { END_MARK, LONG_PASTE_LENGTH, START_MARK, describe, longPaste, parseShare, preview } from './payload.js';

test('the probe text is exactly the length it claims, and both marks survive', () => {
  const text = longPaste();
  assert.equal(text.length, LONG_PASTE_LENGTH);
  assert.ok(text.includes(START_MARK));
  assert.ok(text.endsWith(END_MARK), 'the end mark is the whole point: a truncated share must be visible');
  // A shorter probe is the same shape, which is what makes this testable at all.
  assert.equal(longPaste(100).length, 100);
  assert.ok(longPaste(100).endsWith(END_MARK));
});

test('a long paste that arrives whole is one piece, and one that lost its tail is not', () => {
  const text = longPaste();
  const whole = parseShare({ type: 'text', text }, { platform: 'android' });
  assert.equal(whole.present, true);
  assert.equal(whole.type, 'text');
  assert.equal(whole.length, LONG_PASTE_LENGTH);
  assert.equal(whole.onePiece, true);

  // The failure this asks about: an OS or a bridge that cut the share. The
  // length is the shorter one and the end mark is gone.
  const cut = parseShare({ type: 'text', text: text.slice(0, 4096) }, { platform: 'android' });
  assert.equal(cut.onePiece, false);
  assert.equal(cut.length, 4096);
});

test('a short selection is not asked whether it is in one piece', () => {
  // `null` and not `false`: the question is about the probe paste, and a "no"
  // beside a short selection would read as a truncation nobody measured.
  const read = parseShare({ type: 'text', text: 'a selection from another app' });
  assert.equal(read.onePiece, null);
  assert.equal(read.length, 28);
  assert.equal(read.present, true);
});

test('nothing arrived is a reading of its own, not an empty text', () => {
  for (const raw of [null, undefined]) {
    const read = parseShare(raw);
    assert.deepEqual(read, { present: false, type: null, text: '', length: 0, sender: null, onePiece: null, expected: LONG_PASTE_LENGTH });
  }
});

test('the sending package or bundle is read where this route exposes it, and said not to be where it does not', () => {
  assert.equal(parseShare({ text: 'x', senderPackage: 'com.example.reader' }, { platform: 'android' }).sender, 'com.example.reader');
  assert.equal(parseShare({ text: 'x', meta: { packageName: 'com.example.reader' } }, { platform: 'android' }).sender, 'com.example.reader');
  assert.equal(parseShare({ text: 'x', meta: { bundleIdentifier: 'com.example.reader' } }, { platform: 'ios' }).sender, 'com.example.reader');
  // The honest answer when the platform does not give it: null, and the screen
  // turns that into the sentence rather than into a guessed name.
  assert.equal(parseShare({ text: 'x' }, { platform: 'ios' }).sender, null);
  assert.equal(parseShare({ text: 'x', senderPackage: 'com.example.reader' }, { platform: 'ios' }).sender, null, 'an Android key is not an iOS bundle');
});

test('a shared URL is a text this route carries, and files are counted', () => {
  const url = parseShare({ type: 'weburl', webUrl: 'https://example.invalid/x', files: [{}, {}] });
  assert.equal(url.text, 'https://example.invalid/x');
  assert.equal(url.files, 2);
  assert.equal(url.length, 25);
});

test('the screen\'s lines are the answers, and a long paste is previewed rather than printed', () => {
  const read = parseShare({ type: 'text', text: longPaste() }, { platform: 'android' });
  const lines = new Map(describe(read, { platform: 'android' }));
  assert.equal(lines.get('In one piece (20000 characters)'), 'yes, whole: the length matches and the end mark is there');
  assert.match(lines.get('Text'), /\(20000 characters\)$/, 'the preview says how long the whole thing is');
  assert.ok(lines.get('Text').length < 250, 'a 20,000-character paste must not be printed into a block');

  // ... and the three "did not happen" shapes each say so rather than showing a
  // pass, which is what the task means by "never a fake success".
  const none = new Map(describe(parseShare(null), { platform: 'ios' }));
  assert.match(none.get('What arrived'), /nothing/);
  assert.equal(none.get('Text'), 'UNVERIFIED');
  assert.match(none.get('Sending bundle'), /not exposed by this route/);
  assert.match(none.get('In one piece (20000 characters)'), /UNVERIFIED/);

  const short = new Map(describe(parseShare({ text: 'hi' }), { platform: 'ios' }));
  assert.match(short.get('In one piece (20000 characters)'), /the probe text was not the payload/);
  assert.equal(preview(''), '');
});
