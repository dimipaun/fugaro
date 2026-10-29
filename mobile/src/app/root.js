// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The app's root component, and the shell every later screen hangs on
// (mobile plan, Task P3.1; D114, D125; the demo banner is the owner's ruling of
// 2026-10-19).
//
// **Why this is a function of React and react-native rather than a JSX
// module.** Nothing here can `import 'react'` or `'react-native'`: the Expo
// toolchain is not installed in this environment and no test in this
// repository may need a device (Global Constraint 10). Taking the modules as
// arguments is the same arrangement as the two device ports
// (`expo-secure-store.js`, `expo-async-storage.js`) and it has the same
// payoff: the tree this component builds is asserted in `root.test.js` against
// a fake React, so "the shell renders no secret" is a fact in CI rather than a
// claim in a review. `mobile/index.js` passes the real modules and calls
// `registerRootComponent`.
//
// **What the shell is allowed to show.** The current account's ID and nothing
// else. An id is D114's label for an installation -- the thing the owner typed
// so they can tell two of them apart -- and it is not a secret: it is half the
// name of the Keychain item, so anyone who can read the Keychain already has
// it. The Firebase project, the operator uid and the two items are all
// deliberately absent from the tree, because a tree is the one thing that ends
// up in a screenshot, a crash report and a debug overlay.
//
// **The demo banner, and why it is drawn here.** The owner's ruling requires
// that every screen a reviewer sees while demo mode is on carries "Demo mode:
// sample data", and that there is always a visible way to leave. Both are the
// SHELL's, above the screen and outside its control: a screen added later
// cannot forget them, and `root.test.js` asserts the banner for the first
// screen and the demo screen alike. The body is a slot (`screens.first` /
// `screens.demo`) so that wiring a new screen is an edit to the entry and not
// to this file.
//
// **What comes later, and where.** The body is where the screens hang: **P3.2**
// (the feed reader, `src/feed/`) and **P3.4** (the audio interfaces,
// `src/audio/`) are merged and are in the tree beside this root; **P3.3** (the
// job list), **P3.5** (the voice screen) and **P6.8** (pairing/overview, whose
// seam is `src/app/first-screen.js`) are the tasks still to come, and they are
// what will fill the body. None of them may widen what is rendered here without
// saying so in this file.

import { DEMO_BANNER, LEAVE_DEMO } from '../demo/index.js';

/** The element type names the shell uses, in one place so a test can name
 * them too. They are the react-native primitives the entry passes in. */
export const SHELL = Object.freeze({
  ROOT: 'fugaro.app',
  TITLE: 'fugaro.title',
  BANNER: 'fugaro.banner',
  LEAVE: 'fugaro.banner.leave',
  BODY: 'fugaro.body',
});

/** The title the app shows. A constant, never a value from an account. */
export const APP_TITLE = 'Fugaro';

/** What the shell says when no account is open. The store starts empty (D114:
 * the list is typed by the owner), so this is the first screen a new install
 * shows -- and the first screen the demo is entered from is drawn under it. */
export const NO_ACCOUNT = 'No account yet. Add an installation to begin.';

/**
 * Build the root component.
 *
 * Every argument is required and is checked here rather than trusted: a root
 * built without a store would render "no account" forever, and one built
 * without react-native's primitives would fail at the first render with an
 * error a long way from the cause.
 *
 * @param {object} options
 * @param {object} options.React React, with `createElement`
 * @param {object} options.store the account store (`current()`)
 * @param {Function} options.View react-native's View
 * @param {Function} options.Text react-native's Text
 * @param {Function} options.Pressable react-native's Pressable (the demo's two
 *   controls: "Try the demo" and "Leave demo mode")
 * @param {Function} options.useDemo **the demo's subscription**, as a hook: it
 *   returns the demo's state and re-renders when it changes. `mobile/index.js`
 *   passes `React.useSyncExternalStore(demo.subscribe, demo.state)`; a test
 *   passes a function returning the state it wants rendered. It is an argument
 *   because this file may not import React's hooks (see the header).
 * @param {{ first: Function, demo: Function }} options.screens the two screens
 *   the body can hold, each a function of the demo state
 * @param {Function} options.onLeaveDemo what the banner's way out does (the
 *   demo controller's `leave`)
 */
export function createRoot({ React, store, View, Text, Pressable, useDemo, screens, onLeaveDemo }) {
  if (React === null || typeof React !== 'object' || typeof React.createElement !== 'function') {
    throw new TypeError('createRoot needs React (with createElement)');
  }
  if (store === null || typeof store !== 'object' || typeof store.current !== 'function') {
    throw new TypeError('createRoot needs the account store');
  }
  for (const [name, part] of [['View', View], ['Text', Text], ['Pressable', Pressable]]) {
    if (typeof part !== 'function' && typeof part !== 'object') {
      throw new TypeError(`createRoot needs react-native's ${name}`);
    }
  }
  if (typeof useDemo !== 'function') {
    throw new TypeError('createRoot needs the demo\'s subscription');
  }
  if (screens === null || typeof screens !== 'object'
    || typeof screens.first !== 'function' || typeof screens.demo !== 'function') {
    throw new TypeError('createRoot needs the two screens');
  }
  if (typeof onLeaveDemo !== 'function') {
    throw new TypeError('createRoot needs what leaving the demo does');
  }

  /**
   * The root. `store.current()` is synchronous by design (the switcher sets it
   * and the shell reads it); the account LIST is asynchronous and belongs to
   * the switcher screen, which is not this task's.
   */
  return function Root() {
    const current = store.current();
    const demo = useDemo();
    const active = demo !== null && demo.active === true;

    const children = [
      React.createElement(Text, { type: SHELL.TITLE, key: 'title' }, APP_TITLE),
    ];

    // The banner, on every screen while the demo is on. The words are the demo
    // module's (`DEMO_BANNER`), so the sentence a reviewer reads and the
    // sentence the tests assert are the same string.
    if (active) {
      children.push(React.createElement(
        View,
        { type: SHELL.BANNER, key: 'banner' },
        React.createElement(Text, { type: SHELL.BANNER }, DEMO_BANNER),
        React.createElement(
          Pressable,
          { type: SHELL.LEAVE, onPress: onLeaveDemo },
          LEAVE_DEMO,
        ),
      ));
    }

    // The demo screen when the demo is on, the first screen when it is not.
    // The first screen is where the demo is entered from (its control is not
    // the shell's: it belongs to the screen, and `src/app/first-screen.js`
    // holds the seam P6.8's pairing screen joins).
    const body = active
      ? [screens.demo(demo)]
      : [
        screens.first(demo),
        React.createElement(
          Text,
          { type: SHELL.BODY, key: 'account' },
          current === null ? NO_ACCOUNT : `Account: ${current}`,
        ),
      ];
    children.push(React.createElement(View, { type: SHELL.BODY, key: 'body' }, ...body));

    return React.createElement(View, { type: SHELL.ROOT }, ...children);
  };
}
