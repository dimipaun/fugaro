// The app's root component, and the shell every later screen hangs on
// (mobile plan, Task P3.1; D114, D125).
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
// **What comes later, and where.** The body is where the screens hang: **P3.2**
// (the feed reader, `src/feed/`) and **P3.4** (the audio interfaces,
// `src/audio/`) are merged and are in the tree beside this root; **P3.3** (the
// job list) and **P3.5** (the voice screen) are the Phase 3 tasks still to come,
// and they are what will fill the body. This sentence said P3.4 was later until
// a review read it against the tree -- the same drift `mobile/README.md`'s
// status line and `src/mailbox/`'s header were corrected for in this branch.
// None of them may widen what is rendered here without saying so in this file.

/** The element type names the shell uses, in one place so a test can name
 * them too. They are the react-native primitives the entry passes in. */
export const SHELL = Object.freeze({
  ROOT: 'fugaro.app',
  TITLE: 'fugaro.title',
  BODY: 'fugaro.body',
});

/** The title the app shows. A constant, never a value from an account. */
export const APP_TITLE = 'Fugaro';

/** What the shell says when no account is open. The store starts empty (D114:
 * the list is typed by the owner), so this is the first screen a new install
 * shows. */
export const NO_ACCOUNT = 'No account yet. Add an installation to begin.';

/**
 * Build the root component.
 *
 * Every argument is required and is checked here rather than trusted: a root
 * built without a store would render "no account" forever, and one built
 * without react-native's primitives would fail at the first render with an
 * error a long way from the cause.
 */
export function createRoot({ React, store, View, Text }) {
  if (React === null || typeof React !== 'object' || typeof React.createElement !== 'function') {
    throw new TypeError('createRoot needs React (with createElement)');
  }
  if (store === null || typeof store !== 'object' || typeof store.current !== 'function') {
    throw new TypeError('createRoot needs the account store');
  }
  if (typeof View !== 'function' && typeof View !== 'object') {
    throw new TypeError('createRoot needs react-native\'s View');
  }
  if (typeof Text !== 'function' && typeof Text !== 'object') {
    throw new TypeError('createRoot needs react-native\'s Text');
  }

  /**
   * The root. `store.current()` is synchronous by design (the switcher sets it
   * and the shell reads it); the account LIST is asynchronous and belongs to
   * the switcher screen, which is not this task's.
   */
  return function Root() {
    const current = store.current();
    return React.createElement(
      View,
      { type: SHELL.ROOT },
      React.createElement(Text, { type: SHELL.TITLE }, APP_TITLE),
      React.createElement(
        Text,
        { type: SHELL.BODY },
        current === null ? NO_ACCOUNT : `Account: ${current}`,
      ),
    );
  };
}
