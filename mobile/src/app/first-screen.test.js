// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The first screen's tests (owner ruling of 2026-10-19).
//
// Four properties, and the last two are the ones a later task could break:
//
//   * the screen is honest: it says there is no installation and says what the
//     demo is, before the reviewer taps anything;
//   * the control is the owner's: the words "Try the demo", and pressing it is
//     what enters the demo -- with no setup of any kind, because there is
//     nothing on this screen that could require any;
//   * **the pairing seam holds**: P6.8's screen will be rendered in the slot
//     ABOVE the demo control, and the control stays. The demo exists for the
//     reviewer who cannot pair, so a pairing screen that replaced it would
//     break the ruling it was added beside -- which is why the assertion that
//     both are present at once is here, before P6.8 can arrive without it.
//   * **the ask-your-admin state is the ONE refusal that means the installation
//     would not have this phone** (docs/mobile-auth, Task P4; the spec's step
//     5), and it is drawn beside the demo control rather than in front of it:
//     the fourth test presses the control with the state on, because the
//     mutation that matters is a sign-in that gates the way in for the people
//     the state is about.

import assert from 'node:assert/strict';
import test from 'node:test';

import { TRY_DEMO } from '../demo/index.js';
import { SESSION_CODES } from '../pairing/index.js';
import {
  ASK_YOUR_ADMIN,
  DEMO_HINT,
  FIRST_SCREEN,
  NO_INSTALLATION,
  createFirstScreen,
} from './first-screen.js';

function fakeReact() {
  return {
    createElement(type, props, ...children) {
      return { type, props: props ?? null, children };
    },
  };
}

const View = function View() {};
const Text = function Text() {};
const Pressable = function Pressable() {};

function screen(options = {}) {
  return createFirstScreen({ React: fakeReact(), View, Text, Pressable, onTryDemo: () => {}, ...options });
}

/** A refusal in the shape the pairing flow produces (`session.js`'s `refuse`),
 * so a test names a code rather than a shape it guessed. */
function refusal(code) {
  return Object.freeze({ ok: false, code });
}

/** Everything the tree would show, as one string. */
function render(node) {
  const text = [];
  const walk = (current) => {
    if (typeof current === 'string') {
      text.push(current);
      return;
    }
    if (current === null || typeof current !== 'object') return;
    if (current.type === Text) text.push(...current.children);
    for (const child of current.children ?? []) walk(child);
  };
  walk(node);
  return text.join('\n');
}

function nodesOfType(node, type) {
  const found = [];
  const walk = (current) => {
    if (current === null || typeof current !== 'object') return;
    if (current.props?.type === type) found.push(current);
    for (const child of current.children ?? []) walk(child);
  };
  walk(node);
  return found;
}

test('the screen says what is missing and what the demo is, before anything is tapped', () => {
  const shown = render(screen()());
  assert.equal(shown.includes(NO_INSTALLATION), true);
  assert.equal(shown.includes(DEMO_HINT), true);
  // Nothing a pairing screen owns is claimed here: no account id, no project,
  // no token -- only the two sentences and the control.
  assert.equal(/Account:|uid-|firebase/.test(shown), false, shown);
});

test('the control is the owner\'s words, and pressing it is what enters the demo', () => {
  let tried = 0;
  const tree = screen({ onTryDemo: () => { tried++; } })();
  const controls = nodesOfType(tree, FIRST_SCREEN.TRY_DEMO);
  assert.equal(controls.length, 1);
  assert.deepEqual(controls[0].children, [TRY_DEMO]);
  assert.equal(TRY_DEMO, 'Try the demo');
  controls[0].props.onPress();
  assert.equal(tried, 1);
});

test('the pairing seam is a slot: P6.8\'s screen goes above the demo, and the demo stays', () => {
  // Empty today, and still rendered: an empty slot a test can see is a seam.
  const empty = screen()();
  const slot = nodesOfType(empty, FIRST_SCREEN.PAIRING_SLOT);
  assert.equal(slot.length, 1);
  assert.deepEqual(slot[0].children, [null]);

  // With P6.8's screen in it, BOTH are on the screen: the pairing control and
  // the way in for a reviewer who cannot use it.
  const pairing = () => ({ type: 'stub.pairing', props: { type: 'stub.pairing' }, children: [] });
  const withPairing = screen({ pairingScreen: pairing })();
  assert.deepEqual(nodesOfType(withPairing, 'stub.pairing').length, 1);
  assert.deepEqual(nodesOfType(withPairing, FIRST_SCREEN.TRY_DEMO).length, 1);
  // ... and the seam is the only thing the argument changes.
  assert.equal(render(withPairing).includes(NO_INSTALLATION), true);
});

test('a screen built without its pieces is refused, not defaulted', () => {
  for (const broken of [{ React: undefined }, { View: undefined }, { Text: undefined }, { Pressable: undefined }, { onTryDemo: undefined }]) {
    assert.throws(() => screen(broken), TypeError, String(Object.keys(broken)[0]));
  }
  assert.throws(() => screen({ React: {} }), /React/);
  assert.throws(() => screen({ onTryDemo: 'enter' }), /demo control/);
  assert.throws(() => screen({ pairingScreen: 'later' }), /pairing seam/);
});

// --- the ask-your-admin state (Task P4; the spec's step 5) ------------------

test('the installation’s refusal is the state, and the demo is not behind it', () => {
  // The refusal the exchange answers with is the one that means the
  // installation would not have this phone; the state is the sentence, and the
  // control that needs no account is still there and still works.
  let tried = 0;
  const tree = screen({ pairingRefusal: refusal(SESSION_CODES.SIGN_IN_REFUSED), onTryDemo: () => { tried++; } })();
  assert.deepEqual(
    nodesOfType(tree, FIRST_SCREEN.ASK_YOUR_ADMIN).map((node) => node.children),
    [[ASK_YOUR_ADMIN]],
  );
  // ... the sentence is the spec's, and the words "ask your admin" are in it
  // verbatim: the owner-run check quotes them, and a sentence the app does not
  // draw is a check that cannot pass.
  assert.ok(ASK_YOUR_ADMIN.includes('ask your admin'), ASK_YOUR_ADMIN);
  // The state replaced nothing: the seam, the two sentences and the control are
  // all drawn beside it.
  assert.deepEqual(nodesOfType(tree, FIRST_SCREEN.PAIRING_SLOT)[0].children, [null]);
  const shown = render(tree);
  assert.ok(shown.includes(ASK_YOUR_ADMIN));
  assert.ok(shown.includes(NO_INSTALLATION));
  assert.ok(shown.includes(DEMO_HINT));
  // **The mutation this test is for.** A sign-in -- or the pairing control --
  // that gated the demo would put something between the reviewer and this
  // control; pressing it with the refused state on is what says it did not.
  const controls = nodesOfType(tree, FIRST_SCREEN.TRY_DEMO);
  assert.equal(controls.length, 1, 'the demo control is drawn exactly once, in this state as in every other');
  assert.deepEqual(controls[0].children, [TRY_DEMO]);
  controls[0].props.onPress();
  assert.equal(tried, 1, 'the demo is reachable with no account, whatever the pairing flow answered');
});

test('every other refusal leaves the screen exactly as it was', () => {
  // The state is ONE refusal's, not "the pairing flow refused": a payload this
  // app could not read, a name the owner's list already holds and a store that
  // failed are each a sentence about something else, and each has its own on
  // the pairing screen. Mutation: make the mapping always true and the first
  // line here goes red; make it always false and the test above goes red.
  const notAdmitted = [SESSION_CODES.SIGN_IN_REFUSED];
  for (const code of Object.values(SESSION_CODES)) {
    const tree = screen({ pairingRefusal: refusal(code) })();
    const drawn = nodesOfType(tree, FIRST_SCREEN.ASK_YOUR_ADMIN).length;
    assert.equal(drawn, notAdmitted.includes(code) ? 1 : 0, code);
  }
  // ... and "nothing was handed over" is not a refusal either: a fresh install
  // says what it always said.
  assert.equal(render(screen()()).includes(ASK_YOUR_ADMIN), false);
  assert.equal(nodesOfType(screen()(), FIRST_SCREEN.ASK_YOUR_ADMIN).length, 0);
});

test('something that is not a refusal is refused, not read as one', () => {
  for (const broken of [true, false, 'sign-in-refused', [], {}, { code: '' }, { code: 7 }, { code: null }]) {
    assert.throws(() => screen({ pairingRefusal: broken }), TypeError, JSON.stringify(broken));
  }
});
