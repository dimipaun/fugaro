// The root shell's tests (mobile plan, Task P3.1; D114).
//
// The two properties, both of them about what a leak would look like:
//
//   * the tree renders the CURRENT ACCOUNT'S ID and nothing else from the
//     installation -- a tree is what ends up in a screenshot, a crash report
//     and a React DevTools overlay, so the assertion is that the Firebase
//     project, the operator uid and both items are absent from the serialised
//     tree while the id the owner typed is present;
//   * with nothing open, it says so. The store starts empty (D114: the list is
//     typed by the owner), so this is the first screen of a new install.
//
// React and react-native are not installed (the Expo toolchain is not
// available in this environment), so both are fakes. What is tested is the
// tree this repository builds, not React's renderer.

import assert from 'node:assert/strict';
import test from 'node:test';

import { AccountStore, MemorySecureStore, MemorySettingsStore, ResourceRegistry, SecretStore, SettingsStore, secret } from '../accounts/index.js';
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

function makeStore() {
  return new AccountStore({
    secrets: new SecretStore(new MemorySecureStore()),
    settings: new SettingsStore(new MemorySettingsStore()),
    registry: new ResourceRegistry(),
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

test('with nothing open the shell says so, and names no account', () => {
  const root = createRoot({ React: fakeReact(), store: makeStore(), View, Text });
  const shown = render(root);
  assert.equal(shown.includes(NO_ACCOUNT), true);
  assert.equal(shown.includes(APP_TITLE), true);
});

test('an open account shows its id, and nothing else it was installed with', async () => {
  const store = makeStore();
  await store.add(LAPTOP);
  await store.setSecret('laptop', 'idToken', secret('idToken', ID_TOKEN).secret);
  await store.switchTo('laptop');
  const root = createRoot({ React: fakeReact(), store, View, Text });
  const shown = render(root);
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
  const React = fakeReact();
  const root = createRoot({ React, store: makeStore(), View, Text });
  const tree = root();
  assert.equal(tree.type, View);
  assert.equal(tree.props.type, SHELL.ROOT);
  assert.deepEqual(tree.children.map((child) => child.props.type), [SHELL.TITLE, SHELL.BODY]);
});

test('a root built without its pieces is refused, not defaulted', () => {
  // A root without a store would render "no account" forever, which looks
  // exactly like an empty installation -- so it is an error, not a fallback.
  const ok = { React: fakeReact(), store: makeStore(), View, Text };
  assert.throws(() => createRoot({ ...ok, React: undefined }), /React/);
  assert.throws(() => createRoot({ ...ok, store: undefined }), /account store/);
  assert.throws(() => createRoot({ ...ok, View: undefined }), /View/);
  assert.throws(() => createRoot({ ...ok, Text: undefined }), /Text/);
  assert.throws(() => createRoot({ ...ok, store: { current: 'laptop' } }), /account store/);
});
