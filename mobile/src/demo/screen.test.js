// The demo screen's tests (owner ruling of 2026-10-19).
//
// The screen draws the demo's state and nothing else, so these tests build the
// state the way the app does -- the real controller, entered and confirmed --
// and then read the tree. That is deliberate: a test with a hand-made state
// object would pass just as well if the screen were drawing a fixture, and the
// one thing this screen must never be is a picture of a run that did not
// happen.
//
// What is asserted, in the order a reviewer reads it:
//
//   * the presence rule's reading, the sample installations and jobs;
//   * the waiting question, with the answers offered and the safe one;
//   * the destructive path: the machine's read-back (not the sample's
//     sentence), the phase, the control, and the record the confirmation
//     authorised -- then the refusal a second tap gets, which is shown BESIDE
//     the record rather than instead of it;
//   * the replies, with spoken and acked named separately.

import assert from 'node:assert/strict';
import test from 'node:test';

import { readBack } from '../../../shared/confirm/index.js';
import { createDemoMode } from './index.js';
import { CONFIRM_DESTRUCTIVE, DEMO_SCREEN, NOTHING_YET, createDemoScreen } from './screen.js';
import { DEMO_BANNER, LEAVE_DEMO } from './index.js';

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
  return createDemoScreen({ React: fakeReact(), View, Text, Pressable, onConfirm: () => {}, ...options });
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

/** The nodes of one of this screen's types, in order. */
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

test('the screen shows the app\'s own run: presence, the sample rows, the replies', async () => {
  const demo = createDemoMode();
  const state = await demo.enter();
  const shown = render(screen()(state));

  // P1.4's reading of the heartbeat, and the demo's agent.
  assert.equal(shown.includes('Presence: live (Demo agent)'), true);
  // The sample data, named as what it is.
  assert.equal(shown.includes('Installation: Demo installation (demo-phone)'), true);
  assert.equal(shown.includes('Installation: Demo installation (second phone) (demo-tablet)'), true);
  assert.equal(shown.includes('Job: demo-run-7 on demo-repo -- Docs refactor -- running'), true);
  assert.equal(shown.includes('Job: demo-run-5 on demo-notes -- Release notes -- failed'), true);
  // The question P6.5 is holding, with the answers and the safe one.
  assert.equal(shown.includes('Waiting question: Demo: deploy demo-repo to staging? [answers: yes, no; safe: no]'), true);
  // The replies the client verified, spoke and acked -- all three facts.
  assert.equal(shown.includes('Reply: Demo: two runs on demo-repo, one of them running. [spoken, acked]'), true);
  // What the phone sent, with the ids the client minted.
  assert.equal(shown.includes('Sent: Demo: what is running on demo-repo? [demo-utterance-1]'), true);
});

test('the destructive path shows the machine\'s read-back, then the record the confirmation made', async () => {
  const demo = createDemoMode();
  const entered = await demo.enter();
  const before = render(screen()(entered));

  // The read-back is P6.2's string, built from the resolved target -- not the
  // command. A screen that showed the command twice would be a screen where the
  // second pass says nothing the first did not.
  assert.equal(before.includes(`Read back: ${readBack('cancel', entered.destructive.target)}`), true);
  assert.equal(before.includes(`Read back: ${entered.destructive.command}`), false);
  assert.equal(before.includes('Phase: pending'), true);
  assert.equal(before.includes(CONFIRM_DESTRUCTIVE), true, 'the control is there to tap');
  assert.equal(before.includes('Confirmed:'), false);

  const after = render(screen()(await demo.confirm()));
  assert.equal(after.includes('Confirmed: cancel demo-run-7 (demo-confirmation-1)'), true);
  assert.equal(after.includes('Phase: idle'), true);

  // A second tap is refused by the machine, and the screen shows the refusal
  // BESIDE the record: the action was authorised, and this attempt was not. A
  // screen that showed only the last verdict would tell the reviewer the
  // opposite of one of the two.
  const refused = render(screen()(await demo.confirm()));
  assert.equal(refused.includes('Refused: no-pending-action (actionId)'), true);
  assert.equal(refused.includes('Confirmed: cancel demo-run-7 (demo-confirmation-1)'), true);
});

test('the control is the demo\'s: pressing it is what confirms', async () => {
  const demo = createDemoMode();
  const state = await demo.enter();
  let pressed = 0;
  const shown = screen({ onConfirm: () => { pressed++; demo.confirm(); } })(state);
  const controls = nodesOfType(shown, DEMO_SCREEN.CONFIRM);
  assert.equal(controls.length, 1);
  assert.equal(typeof controls[0].props.onPress, 'function');
  controls[0].props.onPress();
  assert.equal(pressed, 1);
});

test('the screen draws the demo\'s words, and never the banner\'s', async () => {
  // The banner and the way out belong to the shell (`src/app/root.js`), and
  // they are asserted there. The screen drawing them too would be two copies of
  // the one sentence a reviewer must see on every screen.
  const demo = createDemoMode();
  const shown = render(screen()(await demo.enter()));
  assert.equal(shown.includes(DEMO_BANNER), false);
  assert.equal(shown.includes(LEAVE_DEMO), false);
});

test('an inactive demo draws nothing it does not have', () => {
  // The screen is only reached while the demo is on (the shell chooses), but a
  // screen that threw on an empty state would be a crash the day somebody
  // renders it one frame early.
  const shown = render(screen()(createDemoMode().state()));
  assert.equal(shown.includes(NOTHING_YET), true);
  assert.equal(shown.includes('Read back:'), false);
  assert.equal(shown.includes('Confirmed:'), false);
});

test('a screen built without its pieces is refused, not defaulted', () => {
  for (const broken of [{ React: undefined }, { View: undefined }, { Text: undefined }, { Pressable: undefined }, { onConfirm: undefined }]) {
    assert.throws(() => screen(broken), TypeError, String(Object.keys(broken)[0]));
  }
  assert.throws(() => screen({ React: {} }), /React/);
  assert.throws(() => screen({ onConfirm: 'confirm' }), /confirming/);
});
