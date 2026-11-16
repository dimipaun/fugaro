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
//   * **the ask-your-admin sentence** (docs/mobile-auth, Task P4; the spec's
//     step 5) names who can add this phone and is drawn beside the demo control
//     rather than in front of it: the test that presses the control while the
//     sentence is on the screen is the one that fails if a sign-in ever gates
//     the way in for the people the sentence is about;
//   * **and that sentence says what to do, not what happened** -- no refusal in
//     this app means "the installation would not have this phone" (the module's
//     header says why), so the screen must not read one as if it did.

import assert from 'node:assert/strict';
import test from 'node:test';

import { TRY_DEMO } from '../demo/index.js';
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
  // A caller cannot hand this screen an option it does not use and believe it
  // wired something -- **and a pairing refusal is exactly that option**: no code
  // this app answers with means "the installation would not have this phone"
  // (`first-screen.js`'s header), so the screen takes no refusal to read one out
  // of. Mutation: accept a `pairingRefusal` again and this goes red, which is
  // what stops the reviewed defect from returning quietly.
  assert.throws(() => screen({ pairingRefusal: { ok: false, code: 'sign-in-refused' } }), /pairingRefusal/);
});

test('the ask-your-admin sentence stands beside the demo, and the demo is not behind it', () => {
  // Whoever has no installation is owed the name of the person who can change
  // that (the spec's step 5), and the control that needs no account is still
  // there and still works.
  let tried = 0;
  const tree = screen({ onTryDemo: () => { tried++; } })();
  assert.deepEqual(
    nodesOfType(tree, FIRST_SCREEN.ASK_YOUR_ADMIN).map((node) => node.children),
    [[ASK_YOUR_ADMIN]],
  );
  // ... the sentence is the spec's, and the words "ask your admin" are in it
  // verbatim: the owner-run check quotes them, and a sentence the app does not
  // draw is a check that cannot pass.
  assert.ok(ASK_YOUR_ADMIN.includes('ask your admin'), ASK_YOUR_ADMIN);
  // Nothing was replaced: the seam, the two sentences and the control are all
  // drawn beside it, and the sentence is read before the demo it stands beside
  // rather than instead of it.
  assert.deepEqual(nodesOfType(tree, FIRST_SCREEN.PAIRING_SLOT)[0].children, [null]);
  const shown = render(tree);
  assert.ok(shown.includes(ASK_YOUR_ADMIN));
  assert.ok(shown.includes(NO_INSTALLATION));
  assert.ok(shown.includes(DEMO_HINT));
  const order = shown.split('\n');
  assert.ok(order.indexOf(ASK_YOUR_ADMIN) < order.indexOf(DEMO_HINT), 'the sentence is read before the demo it is beside');
  // **The mutation this test is for.** A sign-in -- or the pairing control --
  // that gated the demo would put something between the reviewer and this
  // control; pressing it while the sentence is on the screen is what says it
  // did not, and the same is asserted with P6.8's screen in the seam, which is
  // the pairing control the plan's red step moves "above the demo entry".
  const controls = nodesOfType(tree, FIRST_SCREEN.TRY_DEMO);
  assert.equal(controls.length, 1, 'the demo control is drawn exactly once, in this state as in every other');
  assert.deepEqual(controls[0].children, [TRY_DEMO]);
  controls[0].props.onPress();
  assert.equal(tried, 1, 'the demo is reachable with no account, whatever the screen says about admins');

  let alsoTried = 0;
  const withPairing = screen({
    pairingScreen: () => ({ type: 'stub.pairing', props: { type: 'stub.pairing' }, children: [] }),
    onTryDemo: () => { alsoTried++; },
  })();
  const withPairingControls = nodesOfType(withPairing, FIRST_SCREEN.TRY_DEMO);
  assert.equal(withPairingControls.length, 1, 'the pairing control arriving in the seam does not take the demo away');
  assert.equal(nodesOfType(withPairing, FIRST_SCREEN.ASK_YOUR_ADMIN).length, 1, 'nor the sentence');
  withPairingControls[0].props.onPress();
  assert.equal(alsoTried, 1);
});

test('the ask-your-admin sentence says what to do, not what happened', () => {
  // The app cannot know WHY this phone has no installation: `sign-in-refused` is
  // answered for every failed exchange (an offline phone, a dead or rotated
  // token, a key the project refuses), and v1's allowlist answer is the CLI's,
  // before a QR exists. So the sentence must not read a cause out of anything --
  // and the causes are the words below. Mutation: put "This phone is not on the
  // installation's list yet" back and this goes red.
  assert.equal(
    /\b(?:not on|refus\w*|reject\w*|denied|blocked|expired|revoked)\b/i.test(ASK_YOUR_ADMIN),
    false,
    ASK_YOUR_ADMIN,
  );
  // ... and it names the one action that changes it, in the spec's words.
  assert.ok(ASK_YOUR_ADMIN.includes('ask your admin'), ASK_YOUR_ADMIN);
  // The sentence is the screen's own, so it is drawn on every state of the
  // screen -- the state it describes is "no installation", which is the only
  // state this screen is ever built in.
  for (const options of [{}, { pairingScreen: () => ({ type: 'stub.pairing', props: { type: 'stub.pairing' }, children: [] }) }]) {
    assert.equal(nodesOfType(screen(options)(), FIRST_SCREEN.ASK_YOUR_ADMIN).length, 1);
  }
});
