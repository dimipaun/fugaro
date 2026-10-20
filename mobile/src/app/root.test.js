// The root shell's tests (mobile plan, Task P3.1; D114; the demo banner is the
// owner's ruling of 2026-10-19).
//
// The properties, all of them about what a leak or a missing fence would look
// like:
//
//   * the tree renders the CURRENT ACCOUNT'S ID and nothing else from the
//     installation -- a tree is what ends up in a screenshot, a crash report
//     and a React DevTools overlay, so the assertion is that the Firebase
//     project, the operator uid and both items are absent from the serialised
//     tree while the id the owner typed is present;
//   * with nothing open, it says so. The store starts empty (D114: the list is
//     typed by the owner), so this is the first screen of a new install -- and
//     it is the screen the demo is entered from;
//   * **the demo's banner and its way out are the SHELL's, and they are on
//     every screen**: asserted for the first screen and for the demo screen
//     alike, because the requirement is "a banner on every screen" and a
//     banner that only one screen drew would satisfy neither half of it. The
//     last test here wires the real controller and the real screens to show
//     the two together: tap "Try the demo", the banner appears, tap "Leave
//     demo mode", it goes.
//
// React and react-native are not installed (the Expo toolchain is not
// available in this environment), so both are fakes. What is tested is the
// tree this repository builds, not React's renderer. The demo's own state is
// injected through the `useDemo` hook -- which is how `mobile/index.js` passes
// it, and which is why the shell may not import React's hooks itself.

import assert from 'node:assert/strict';
import test from 'node:test';

import { AccountStore, MemorySecureStore, MemorySettingsStore, ResourceRegistry, SecretStore, SettingsStore, secret } from '../accounts/index.js';
import { createDemoScreen } from '../demo/screen.js';
import { DEMO_BANNER, LEAVE_DEMO, TRY_DEMO, createDemoMode } from '../demo/index.js';
import { createFirstScreen } from './first-screen.js';
import { APP_TITLE, NO_ACCOUNT, SHELL, createRoot } from './root.js';

const ID_TOKEN = 'synthetic-id-token';
const LAPTOP = {
  id: 'laptop',
  firebaseProjectId: 'fugaro-dev',
  authUid: 'uid-0001',
  fugaroProject: 'fugaro',
};

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
const Pressable = function Pressable() {};

function makeStore() {
  return new AccountStore({
    secrets: new SecretStore(new MemorySecureStore()),
    settings: new SettingsStore(new MemorySettingsStore()),
    registry: new ResourceRegistry(),
  });
}

/** The two screens, as stubs that say which one was asked for. */
function stubScreens(seen = []) {
  return {
    seen,
    screens: {
      first: (state) => {
        seen.push(['first', state]);
        return { type: 'stub.first', props: { type: 'stub.first' }, children: [] };
      },
      demo: (state) => {
        seen.push(['demo', state]);
        return { type: 'stub.demo', props: { type: 'stub.demo' }, children: [] };
      },
    },
  };
}

/** The pieces every root needs, with the demo inactive unless told otherwise. */
function shellFor({ React = fakeReact(), store = makeStore(), state = { active: false }, screens, onLeaveDemo = () => {} } = {}) {
  return createRoot({
    React,
    store,
    View,
    Text,
    Pressable,
    useDemo: () => state,
    screens: screens ?? stubScreens().screens,
    onLeaveDemo,
  });
}

/** Everything the tree would show, as one string. */
function render(root) {
  const text = [];
  const walk = (node) => {
    if (typeof node === 'string') {
      text.push(node);
      return;
    }
    if (node === null || typeof node !== 'object') return;
    if (node.type === Text) text.push(...node.children);
    for (const child of node.children ?? []) walk(child);
  };
  walk(root());
  return text.join('\n');
}

/** Every node of the tree whose `type` is one of the shell's, in order. */
function nodesOfType(root, type) {
  const found = [];
  const walk = (node) => {
    if (node === null || typeof node !== 'object') return;
    if (node.props?.type === type) found.push(node);
    for (const child of node.children ?? []) walk(child);
  };
  walk(root());
  return found;
}

test('with nothing open the shell says so, and names no account', () => {
  const shown = render(shellFor());
  assert.equal(shown.includes(NO_ACCOUNT), true);
  assert.equal(shown.includes(APP_TITLE), true);
});

test('an open account shows its id, and nothing else it was installed with', async () => {
  const store = makeStore();
  await store.add(LAPTOP);
  await store.setSecret('laptop', 'idToken', secret('idToken', ID_TOKEN).secret);
  await store.switchTo('laptop');
  const shown = render(shellFor({ store }));
  assert.equal(shown.includes('Account: laptop'), true);
  // The id is D114's label for an installation, and it is not a secret: it is
  // half the name of the Keychain item, so anyone who can read the Keychain
  // already has it. Everything else the installation was built from is not.
  for (const value of [ID_TOKEN, LAPTOP.firebaseProjectId, LAPTOP.authUid, LAPTOP.fugaroProject]) {
    assert.equal(shown.includes(value), false, value);
  }
  assert.equal(shown.includes(NO_ACCOUNT), false, 'an open account is not "no account"');
});

test('the tree is built from the primitives it was given', () => {
  const root = shellFor();
  const tree = root();
  assert.equal(tree.type, View);
  assert.equal(tree.props.type, SHELL.ROOT);
  assert.deepEqual(tree.children.map((child) => child.props.type), [SHELL.TITLE, SHELL.BODY]);
});

test('the demo is off: no banner, no way out, and the first screen', () => {
  const { seen, screens } = stubScreens();
  const tree = shellFor({ screens })();
  assert.deepEqual(seen.map(([which]) => which), ['first'], 'the first screen was the one asked for');
  assert.deepEqual(nodesOfType(() => tree, SHELL.BANNER), []);
  assert.deepEqual(nodesOfType(() => tree, SHELL.LEAVE), []);
  assert.equal(render(() => tree).includes(DEMO_BANNER), false);
  assert.equal(render(() => tree).includes(TRY_DEMO), false, 'the control belongs to the first screen');
});

test('the demo is on: the banner and the way out are on the screen, whatever it holds', () => {
  // The requirement is a banner on EVERY screen, so it is asserted from the
  // shell -- which draws it outside the body -- rather than from a screen.
  const state = { active: true };
  const { seen, screens } = stubScreens();
  let left = 0;
  const tree = shellFor({ screens, state, onLeaveDemo: () => { left++; } })();
  assert.deepEqual(seen.map(([which]) => which), ['demo'], 'the demo screen was the one asked for');
  assert.equal(seen[0][1], state, 'the screen was handed the state the hook returned');

  const shown = render(() => tree);
  assert.equal(shown.includes(DEMO_BANNER), true);
  assert.equal(DEMO_BANNER, 'Demo mode: sample data');
  assert.equal(shown.includes(LEAVE_DEMO), true);
  // ... and the way out works: an always-visible control is not a control if
  // pressing it does nothing.
  const leave = nodesOfType(() => tree, SHELL.LEAVE);
  assert.equal(leave.length, 1);
  assert.equal(typeof leave[0].props.onPress, 'function');
  leave[0].props.onPress();
  assert.equal(left, 1);
  assert.equal(shown.includes(NO_ACCOUNT), false, 'the demo does not claim there is no account');
});

test('a root built without its pieces is refused, not defaulted', () => {
  // A root without a store would render "no account" forever, which looks
  // exactly like an empty installation -- so it is an error, not a fallback.
  const ok = { React: fakeReact(), store: makeStore(), View, Text, Pressable };
  const rest = { useDemo: () => ({ active: false }), screens: stubScreens().screens, onLeaveDemo: () => {} };
  assert.throws(() => createRoot({ ...ok, ...rest, React: undefined }), /React/);
  assert.throws(() => createRoot({ ...ok, ...rest, store: undefined }), /account store/);
  assert.throws(() => createRoot({ ...ok, ...rest, View: undefined }), /View/);
  assert.throws(() => createRoot({ ...ok, ...rest, Text: undefined }), /Text/);
  assert.throws(() => createRoot({ ...ok, ...rest, Pressable: undefined }), /Pressable/);
  assert.throws(() => createRoot({ ...ok, ...rest, store: { current: 'laptop' } }), /account store/);
  // The demo's subscription and the two screens are required too: a shell with
  // no way to hear the demo, or with no screen to draw, is a shell that renders
  // a banner over nothing.
  assert.throws(() => createRoot({ ...ok, ...rest, useDemo: undefined }), /subscription/);
  assert.throws(() => createRoot({ ...ok, ...rest, screens: { first: rest.screens.first } }), /two screens/);
  assert.throws(() => createRoot({ ...ok, ...rest, onLeaveDemo: undefined }), /leaving/);
});

test('wired for real: the demo\'s control enters it, and the banner appears on the app', async () => {
  // The end-to-end wiring, with the REAL controller, the REAL screens and the
  // fake React: this is what "Try the demo on the first screen" means from the
  // reviewer's side, and it is asserted here because it is the shell that joins
  // the two halves (a screen's control, and the banner over every screen).
  const React = fakeReact();
  const demo = createDemoMode();
  // A store that re-renders: the shell asks the hook, the hook asks the demo.
  let version = 0;
  const useDemo = () => {
    version += 1;
    return demo.state();
  };
  const screens = {
    first: createFirstScreen({
      React, View, Text, Pressable, onTryDemo: () => demo.enter(), pairingScreen: null,
    }),
    demo: createDemoScreen({
      React, View, Text, Pressable, onConfirm: () => demo.confirm(),
    }),
  };
  const root = createRoot({
    React, store: makeStore(), View, Text, Pressable, useDemo, screens, onLeaveDemo: () => demo.leave(),
  });

  assert.equal(render(root).includes(TRY_DEMO), true, 'the first screen offers the demo');
  assert.equal(render(root).includes(DEMO_BANNER), false);

  const control = nodesOfType(root, 'fugaro.first.try-demo')[0];
  await control.props.onPress();

  const shown = render(root);
  assert.equal(shown.includes(DEMO_BANNER), true, 'every screen carries the banner');
  assert.equal(shown.includes('Demo installation'), true, 'the sample installations are on it');
  assert.equal(shown.includes('demo-run-7'), true);
  assert.equal(shown.includes(TRY_DEMO), false, 'the way in is not offered from inside');
  assert.equal(version > 0, true, 'the shell asked the demo for its state');

  nodesOfType(root, SHELL.LEAVE)[0].props.onPress();
  const after = render(root);
  assert.equal(after.includes(DEMO_BANNER), false);
  assert.equal(after.includes(TRY_DEMO), true, 'leaving puts the first screen back');
});
