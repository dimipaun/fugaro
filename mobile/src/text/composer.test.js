// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The composer: the draft that outlives the view (mobile identity and pairing
// plan, Task X2; the spec's "Long text input").
//
// The task's own red step is this file's first test: **a 100000-character paste
// must survive a mode switch**. It is written against the text screen itself --
// `createTextScreen` over a fake React, the way `screen.test.js` walks it --
// because the claim is about what a phone would show after the view was torn
// down and built again, not about a string a test holds. The draft is read from
// the composer on every render; a draft kept in the view's own state is exactly
// what the second render would lose.
//
// The four sentences this file exists for:
//
//   1. **a 100000-character paste is held whole** across a mode switch and a
//      rotation (the two rebuilds a view cannot survive on its own state);
//   2. **the draft is verbatim** -- newlines, unicode and surrounding space are
//      the owner's, and this store neither trims nor normalises nor caps;
//   3. **a value that is not a string is refused and the draft is kept**, so a
//      caller's mistake cannot wipe what somebody pasted;
//   4. **`clear` is the caller's**, and the subscribers hear every change -- the
//      store is the state, and a view that kept a second copy is the copy that
//      loses a keystroke.
//
// React and react-native are not installed (the Expo toolchain is not in this
// environment), so the three primitives are fakes and the screen is walked as a
// tree. The fixtures are synthetic: a paste the test builds, and no value from
// any installation.

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  COMPOSER_CODES,
  INPUT_HINT,
  LONG_PASTE_CHARS,
  SCREEN,
  createComposer,
  createTextScreen,
} from './index.js';

// ---------------------------------------------------------------------------
// The text screen, as a tree a phone would render.
// ---------------------------------------------------------------------------

const fakeReact = {
  createElement(type, props, ...children) {
    return { type, props: props ?? null, children };
  },
};

const View = function View() {};
const Text = function Text() {};
const TextInput = function TextInput() {};

const TextScreen = createTextScreen({ React: fakeReact, View, Text, TextInput });

/** Every element in the tree, depth first. */
function walk(node, found = []) {
  if (node === null || typeof node !== 'object') return found;
  found.push(node);
  for (const child of node.children ?? []) walk(child, found);
  return found;
}

/** The `TextInput` the screen built -- the field the owner types into. */
function inputOf(props) {
  return walk(TextScreen(props)).find((one) => one.type === TextInput);
}

/** The draft the field would show, read out of the tree rather than the store. */
function shownDraft(composer) {
  const field = inputOf({ draft: composer.text(), onChangeText: composer.change });
  return field.props.value;
}

/** A paste of exactly the plan's figure, with a distinct last character so a
 * truncation of any length is caught by the equality below. */
function longPaste(size = LONG_PASTE_CHARS) {
  return `${'a'.repeat(size - 1)}z`;
}

// ---------------------------------------------------------------------------
// 1. The red step: a 100000-character paste survives a mode switch.
// ---------------------------------------------------------------------------

test('the red step: a 100000-character paste survives a mode switch and a rotation', () => {
  const composer = createComposer();
  const paste = longPaste();

  assert.equal(composer.change(paste).ok, true, 'the paste is taken');
  assert.equal(composer.length(), LONG_PASTE_CHARS, 'in full');
  assert.equal(shownDraft(composer), paste, 'and the field shows it');

  // The view is rebuilt twice, and neither rebuild is handed the first render's
  // value: this is a switch to the voice view and back (the text view torn down
  // and built again) and a rotation (the same screen, rebuilt). Both read the
  // draft from the composer, which is what "the view holds nothing" means.
  assert.equal(shownDraft(composer), paste, 'the mode switch lost nothing');
  assert.equal(shownDraft(composer), paste, 'and the rotation lost nothing');
  assert.equal(composer.text().length, LONG_PASTE_CHARS, 'the store still has every character');
  assert.equal(composer.text(), paste, 'verbatim, not a shortened copy');
});

// ---------------------------------------------------------------------------
// 2. The draft is the owner's, verbatim.
// ---------------------------------------------------------------------------

test('the draft is verbatim: newlines, unicode and surrounding space are kept', () => {
  const composer = createComposer();
  const spec = '  # Task: ship it\n\n- one\n- two — “quoted” 🙂\n\n';

  assert.equal(composer.change(spec).ok, true);
  assert.equal(composer.text(), spec, 'nothing is trimmed, normalised or re-encoded');
  assert.equal(composer.length(), spec.length, 'and the length is the string\'s own');
});

test('an empty change is a value like any other: the owner may delete the draft', () => {
  const composer = createComposer();
  composer.change('something');
  assert.equal(composer.change('').ok, true);
  assert.equal(composer.text(), '', 'a cleared field is an empty draft, not a refusal');
  assert.equal(composer.length(), 0);
});

// ---------------------------------------------------------------------------
// 3. A bad value is refused, and the draft is kept.
// ---------------------------------------------------------------------------

test('a value that is not a string is refused and the draft is kept', () => {
  const composer = createComposer();
  composer.change('what I pasted');

  for (const bad of [42, null, undefined, {}, ['a'], true]) {
    const refused = composer.change(bad);
    assert.equal(refused.ok, false, `${String(bad)} is refused`);
    assert.equal(refused.code, COMPOSER_CODES.BAD_TEXT);
    assert.equal(refused.field, 'text');
  }
  assert.equal(composer.text(), 'what I pasted', 'and nothing wiped the draft');
});

// ---------------------------------------------------------------------------
// 4. The view re-reads; the caller clears.
// ---------------------------------------------------------------------------

test('a subscriber hears every change, and unsubscribing stops it', () => {
  const composer = createComposer();
  const heard = [];
  const subscribed = composer.subscribe((state) => heard.push([state.text, state.length]));
  assert.equal(subscribed.ok, true);

  composer.change('one');
  composer.change('one two');
  subscribed.unsubscribe();
  composer.change('one two three');

  assert.deepEqual(heard, [['one', 3], ['one two', 7]], 'the changes it was subscribed for, and no more');
  assert.equal(composer.text(), 'one two three', 'the store kept the change nobody heard');
});

test('a subscriber that threw does not stop the others, and the change still lands', () => {
  const composer = createComposer();
  const heard = [];
  composer.subscribe(() => {
    throw new Error('a view with a fault');
  });
  composer.subscribe((state) => heard.push(state.text));

  assert.equal(composer.change('still theirs').ok, true);
  assert.deepEqual(heard, ['still theirs'], 'the second subscriber heard it');
  assert.equal(composer.text(), 'still theirs');
});

test('a subscriber that is not a listener is refused', () => {
  const composer = createComposer();
  const refused = composer.subscribe('not a function');
  assert.equal(refused.ok, false);
  assert.equal(refused.code, COMPOSER_CODES.BAD_LISTENER);
  assert.equal(refused.field, 'listener');
});

test('clear empties the draft and tells the view it changed', () => {
  const composer = createComposer();
  const heard = [];
  composer.subscribe((state) => heard.push(state.length));
  composer.change('sent, and accepted');

  assert.deepEqual(composer.clear(), { ok: true, length: 0 });
  assert.equal(composer.text(), '', 'the draft is gone');
  assert.deepEqual(heard, ['sent, and accepted'.length, 0],
    'and the view was told, so it can draw the empty field');
});

// ---------------------------------------------------------------------------
// 5. The wiring is two props, and nothing else.
// ---------------------------------------------------------------------------

test('the screen\'s draft is the composer\'s text and its handler is the composer\'s change', () => {
  const composer = createComposer();
  const field = inputOf({ draft: composer.text(), onChangeText: composer.change });

  assert.equal(field.props.value, '', 'an untouched composer is an empty field');
  assert.equal(field.props.placeholder, INPUT_HINT, 'and it still says what the field is for');
  assert.equal(field.props.type, SCREEN.INPUT, 'the field is the screen\'s own input');
  // The handler the screen was handed is the store's own `change`: a keystroke
  // reaches the draft with no adapter in between, which is what keeps the view
  // from holding a second copy of it.
  assert.equal(field.props.onChangeText, composer.change);
});
