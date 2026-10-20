// The first screen's tests (owner ruling of 2026-10-19).
//
// Three properties, and the third is the one a later task could break:
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

import assert from 'node:assert/strict';
import test from 'node:test';

import { TRY_DEMO } from '../demo/index.js';
import {
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
});
