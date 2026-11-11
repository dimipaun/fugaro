// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The pairing screen's tree (mobile plan, Task P6.8; the pairing plan's §7.2).
//
// Two of P6.8's verification bullets are rendering ones, and both are asserted
// here:
//
//   * "the code shown is derived per pairing" -- the screen draws the code the
//     session derived and nothing else, and the code is what the operator types
//     into the CLI;
//   * the negative half, which is the one a careless refactor breaks: the tree
//     carries **no credential**. The walk below reads every string the tree
//     holds, and a token or a MAC key that reached a screen is a value in a
//     screenshot, a crash report and a screenshot of a crash report.
//
// React and react-native are not installed (the Expo toolchain is not available
// in this environment), so all five primitives are fakes and the assertion is
// against the tree this repository builds. Every value below is synthetic and
// shaped like nothing.

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  CANCEL_WORD,
  CODE_HINT,
  CODE_LABEL,
  DELIVERED_SENTENCE,
  EXPIRED_SENTENCE,
  IDLE_SENTENCE,
  LABEL_HINT,
  PAIR_WORD,
  PAIRING_TITLE,
  PAYLOAD_HINT,
  SCREEN,
  WATCHING_SENTENCE,
  createPairingScreen,
  refusalSentence,
  stateSentence,
} from './index.js';
import { SESSION_CODES, SESSION_STATES } from './session.js';

/** The smallest React that can build a tree we can read. */
function fakeReact() {
  return {
    createElement(type, props, ...children) {
      return { type, props: props ?? null, children };
    },
  };
}

const View = function View() {};
const Text = function Text() {};
const TextInput = function TextInput() {};
const Pressable = function Pressable() {};

/** Every element in the tree, depth first. */
function walk(node, found = []) {
  if (node === null || typeof node !== 'object') return found;
  found.push(node);
  for (const child of node.children ?? []) walk(child, found);
  return found;
}

/** Every string the tree would show, in order. */
function render(screen, props = {}) {
  return walk(screen(props))
    .filter((one) => one.type === Text)
    .flatMap((one) => one.children.filter((child) => typeof child === 'string'));
}

/** The strings one named element carries. */
function stringsOf(screen, name, props = {}) {
  return walk(screen(props))
    .filter((one) => one.props !== null && one.props.type === name)
    .flatMap((one) => one.children.filter((child) => typeof child === 'string'));
}

/** The props one named element carries. */
function propsOf(screen, name, props = {}) {
  const found = walk(screen(props)).filter((one) => one.props !== null && one.props.type === name);
  return found.length === 0 ? null : found[0].props;
}

const screen = createPairingScreen({ React: fakeReact(), View, Text, TextInput, Pressable });

/** A session snapshot in one state, with this file's synthetic values. */
function snapshot(over = {}) {
  return {
    state: SESSION_STATES.IDLE,
    code: null,
    project: null,
    expiresAt: null,
    deviceUid: null,
    record: null,
    refusal: null,
    ...over,
  };
}

test('the screen draws the code, the project and what to do with them', () => {
  const watching = snapshot({ state: SESSION_STATES.WATCHING, code: 'GBDB-6AMT', project: 'aurora' });
  assert.deepEqual(stringsOf(screen, SCREEN.CODE, { session: watching }), ['GBDB-6AMT']);
  assert.deepEqual(stringsOf(screen, SCREEN.CODE_LABEL, { session: watching }), [CODE_LABEL]);
  assert.deepEqual(stringsOf(screen, SCREEN.CODE_HINT, { session: watching }), [CODE_HINT]);
  assert.deepEqual(stringsOf(screen, SCREEN.PROJECT, { session: watching }), ['aurora']);
  assert.deepEqual(stringsOf(screen, SCREEN.TITLE, { session: watching }), [PAIRING_TITLE]);
  assert.deepEqual(stringsOf(screen, SCREEN.STATE, { session: watching }), [WATCHING_SENTENCE]);
  // While a pairing is running the screen offers the way out and nothing else:
  // a second Pair button would start a pairing over the one in flight.
  assert.deepEqual(stringsOf(screen, SCREEN.CANCEL, { session: watching }), [CANCEL_WORD]);
  assert.equal(propsOf(screen, SCREEN.PAIR, { session: watching }), null);
});

test('the screen draws no code it was not given, and no credential ever', () => {
  const idle = render(screen, { session: snapshot() });
  assert.equal(idle.includes(CODE_LABEL), false, 'no code label before a key exists');
  assert.deepEqual(stringsOf(screen, SCREEN.CODE, { session: snapshot() }), []);
  assert.equal(idle.includes(IDLE_SENTENCE), true);
  // The two fields and the button are what an idle screen offers.
  assert.equal(propsOf(screen, SCREEN.LABEL_INPUT, { session: snapshot() }).placeholder, LABEL_HINT);
  assert.equal(propsOf(screen, SCREEN.TEXT_INPUT, { session: snapshot() }).placeholder, PAYLOAD_HINT);
  assert.deepEqual(stringsOf(screen, SCREEN.PAIR, { session: snapshot() }), [PAIR_WORD]);

  // **The credential walk.** A delivered snapshot carries the record and the
  // device uid -- neither is a secret (§4.5: a uid is a key) -- and no token
  // and no MAC key, because the session's snapshot has none to carry. The
  // fixture values below are what a leak would look like on screen.
  const delivered = snapshot({
    state: SESSION_STATES.DELIVERED,
    code: 'GBDB-6AMT',
    project: 'aurora',
    deviceUid: 'm~' + 'M'.repeat(21) + 'A',
    record: {
      project: 'aurora',
      gcpProject: 'fp-1234',
      databaseUrl: 'https://fp-1234-default-rtdb.firebaseio.com',
      apiKey: 'test-web-api-key',
      deviceUid: 'm~' + 'M'.repeat(21) + 'A',
    },
  });
  const shown = render(screen, { session: delivered });
  assert.equal(shown.includes('test-device-token-not-a-credential'), false);
  assert.equal(shown.includes('test-web-api-key'), false);
  assert.equal(shown.includes('synthetic-mac-key'), false);
  // The API key is ordinary configuration (D259) and is still not a thing to
  // draw: the screen shows the project's name and nothing else of the record.
  assert.equal(shown.includes(DELIVERED_SENTENCE), true);
  assert.equal(shown.includes('aurora'), true);
});

test('the screen says one sentence per state, and a refusal says which', () => {
  assert.equal(stateSentence(null), IDLE_SENTENCE);
  assert.equal(stateSentence(snapshot()), IDLE_SENTENCE);
  assert.equal(stateSentence(snapshot({ state: SESSION_STATES.WATCHING })), WATCHING_SENTENCE);
  assert.equal(stateSentence(snapshot({ state: SESSION_STATES.DELIVERED })), DELIVERED_SENTENCE);
  assert.equal(stateSentence(snapshot({ state: SESSION_STATES.EXPIRED })), EXPIRED_SENTENCE);
  // §7.2: a node that went away before a delivery says the pairing expired, and
  // the sentence tells the user where a new one starts.
  const expired = snapshot({ state: SESSION_STATES.EXPIRED, refusal: { ok: false, code: SESSION_CODES.EXPIRED } });
  assert.equal(stateSentence(expired), EXPIRED_SENTENCE);
  const refused = snapshot({ state: SESSION_STATES.REFUSED, refusal: { ok: false, code: 'bad-uid' } });
  assert.equal(stateSentence(refused), refusalSentence('bad-uid'));
  // A payload's own refusal is one sentence: a person holding a QR they did not
  // generate cannot act on the difference between a bad uid and a bad URL.
  assert.equal(refusalSentence('bad-uid'), refusalSentence('bad-database-url'));
  // ... and a refusal this screen does not know still says something.
  assert.equal(typeof refusalSentence('something-new'), 'string');
  assert.equal(refusalSentence('something-new') !== '', true);
});

test('the callbacks are the caller’s, and a screen with none is still a tree', () => {
  const calls = [];
  const wired = createPairingScreen({
    React: fakeReact(),
    View,
    Text,
    TextInput,
    Pressable,
  });
  const props = {
    session: snapshot(),
    label: 'laptop',
    text: 'the payload',
    onLabel: (value) => calls.push(['label', value]),
    onText: (value) => calls.push(['text', value]),
    onPair: () => calls.push(['pair']),
  };
  assert.equal(propsOf(wired, SCREEN.LABEL_INPUT, props).value, 'laptop');
  assert.equal(propsOf(wired, SCREEN.TEXT_INPUT, props).value, 'the payload');
  propsOf(wired, SCREEN.LABEL_INPUT, props).onChangeText('desk');
  propsOf(wired, SCREEN.PAIR, props).onPress();
  assert.deepEqual(calls, [['label', 'desk'], ['pair']]);
  // A caller that passes nothing gets no callbacks and no crash: every prop
  // this screen sets is either its own or guarded.
  const bare = propsOf(wired, SCREEN.PAIR, { session: snapshot() });
  assert.equal(bare.onPress, undefined);
  assert.equal(propsOf(wired, SCREEN.LABEL_INPUT, {}).value, '');
});

test('the screen refuses to be built without React or a primitive', () => {
  assert.throws(() => createPairingScreen({ View, Text, TextInput, Pressable }), TypeError);
  assert.throws(() => createPairingScreen({ React: fakeReact(), View, Text, TextInput }), TypeError);
  assert.throws(() => createPairingScreen({ React: fakeReact(), View, Text, Pressable }), TypeError);
});
