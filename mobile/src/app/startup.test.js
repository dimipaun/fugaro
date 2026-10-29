// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The guarded startup and the root error boundary (added 2026-10-21, with the
// Android launch crash).
//
// **What went wrong, and what this file is the answer to.** The crash the owner
// reported was a `TypeError` thrown while `mobile/index.js` was being
// **evaluated** -- before `registerRootComponent`, so before React, so before
// any screen: the phone showed nothing at all and the app closed. The cause is
// fixed where it was (`src/accounts/expo-secure-store.js`), but the *shape* of
// the failure is the thing this task is also about: a startup that cannot
// announce itself. A build on a phone has no terminal, no `console`, and no red
// box -- in a release build an exception during module evaluation is a silent
// exit -- so the next one of these must be **readable on the device**.
//
// Two pieces, and they catch different things:
//
//   * `createGuardedStartup` wraps the wiring: `mobile/index.js` builds the
//     ports, the store, the demo and the screens **inside** `root(build)`, so a
//     throw there is a report and not a crash. It also exposes `report`, which
//     is the same door for an **async** startup step -- the app's first one
//     today is entering the demo -- because a rejected promise at startup is
//     the failure a guarded `try` around synchronous wiring would miss.
//   * `createErrorBoundary` catches a **render-time** throw anywhere below the
//     root, which React cannot catch any other way (a class component is the
//     only mechanism it offers for this).
//
// Both render the same plain screen: what failed, the message, and a
// **"Copy details" line** the reviewer can long-press and paste into a bug
// report.
//
// **How the assertions are made.** React and react-native are not installed in
// CI and no test here may need a device (Global Constraint 10), so React is a
// fake with a `createElement` that builds a readable tree and a minimal
// `Component` -- and the two class components this file's subject returns are
// instantiated **directly**, the way `root.test.js` calls its root. What is
// asserted is the tree this repository builds, not React's renderer.
//
// The positive control in every group is a planted failure: a `build` that
// throws, a thrown non-`Error`, a render that throws. A green run therefore
// means the guard rendered a screen, not that it never had to.

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  COPY_DETAILS,
  FAILURE,
  FAILURE_HINT,
  FAILURE_TITLE,
  createErrorBoundary,
  createFailureScreen,
  createGuardedStartup,
  failureDetails,
} from './startup.js';

/** The smallest React that can build a tree we can read, plus the two members
 * the class components need. `setState` is the state update React does (a
 * shallow merge), kept minimal on purpose. */
function fakeReact() {
  class Component {
    constructor(props) {
      this.props = props;
    }

    setState(next) {
      this.state = { ...(this.state ?? {}), ...next };
    }
  }
  return {
    Component,
    // `props` carries `children`, the way React's own elements do, so a class
    // component that reads `this.props.children` sees what it was given.
    createElement(type, props, ...children) {
      return { type, props: { ...(props ?? {}), children }, children };
    },
  };
}

const View = function View() {};
const Text = function Text() {};

/**
 * One element as React would have it: a function component is **called**, a
 * class component is **instantiated** and asked for its element. This file's
 * subject builds both kinds (the screen is a function, the boundary and the
 * guarded root are classes), so a walker that only read `children` off the
 * returned element would see `[]` and report a blank screen -- which is exactly
 * the reading that must not be possible here.
 */
function expand(node) {
  if (node === null || typeof node !== 'object') return node;
  if (node.type === View || node.type === Text) return node;
  if (typeof node.type !== 'function') return node;
  const props = node.props ?? {};
  if (typeof node.type.prototype?.render === 'function') return expand(new node.type(props).render());
  return expand(node.type(props));
}

/** Every node of an element tree, each one expanded, in order. */
function* walk(node) {
  if (node === null || typeof node !== 'object') return;
  const element = expand(node);
  if (element === null || typeof element !== 'object') return;
  yield element;
  for (const child of element.children ?? []) yield* walk(child);
}

/** Everything the tree would show, as one string. */
function render(node) {
  const text = [];
  for (const element of walk(node)) {
    if (element.type === Text) text.push(...element.children.filter((child) => typeof child === 'string'));
  }
  return text.join('\n');
}

/** Every node of the tree whose `type` prop is one of the failure screen's. */
function nodesOfType(node, type) {
  const found = [];
  for (const element of walk(node)) {
    if (element.props?.type === type) found.push(element);
  }
  return found;
}

// --- failureDetails: a thrown value, made readable ---------------------------

test('a thrown Error becomes a readable record, and the value is not hidden', () => {
  const error = new TypeError('expo-secure-store module is missing WHEN_UNLOCKED_THIS_DEVICE_ONLY');
  const details = failureDetails(error, 'wiring the app');
  assert.equal(details.what, 'wiring the app');
  assert.equal(details.name, 'TypeError');
  assert.equal(details.message, 'expo-secure-store module is missing WHEN_UNLOCKED_THIS_DEVICE_ONLY');
  assert.match(details.text, /TypeError/);
  assert.match(details.text, /WHEN_UNLOCKED_THIS_DEVICE_ONLY/, 'the message the reviewer needs is in the copyable text');
  assert.equal(typeof details.text, 'string');
  // Mutation: drop the message from `text` and this goes red; that string is the
  // whole reason the screen exists.
  assert.notEqual(details.text.includes(details.message), false);
});

test('anything can be thrown, and describing it must not throw too', () => {
  // The guard is a fence around a failure, so it may not have a failure mode of
  // its own: a startup can throw a string, `undefined`, a plain object -- and a
  // circular one, which is where `JSON.stringify` alone would throw.
  const circular = { kind: 'wiring' };
  circular.self = circular;
  const cases = [
    ['a string', 'the module is not linked', /the module is not linked/],
    ['undefined', undefined, /undefined/],
    ['null', null, /null/],
    ['a number', 42, /42/],
    ['a bare object', { code: 'ENOLINK' }, /ENOLINK/],
    ['a circular object', circular, /\[object Object\]|circular/],
    ['an Error with no message', new Error(''), /\(no message\)/],
  ];
  for (const [what, thrown, expected] of cases) {
    const details = failureDetails(thrown, 'wiring the app');
    assert.match(details.text, expected, `${what} must be described`);
    assert.equal(details.what, 'wiring the app');
    // Every case names what was thrown, so a report is never just a blank.
    assert.equal(details.name.length > 0, true, `${what} must have a name`);
  }
  // Mutation: use `error.message` unguarded and the string/undefined/circular
  // cases above go red with a `TypeError` out of the fence itself.
  assert.equal(failureDetails(circular).message.includes('circular'), true);
});

// --- the screen -------------------------------------------------------------

test('the failure screen says what failed, the message, and offers the details to copy', () => {
  const React = fakeReact();
  const error = new Error('Unable to resolve module ../../../shared/confirm/index.js');
  const Screen = createFailureScreen({ React, View, Text, error, what: 'wiring the app' });
  const tree = Screen({});
  const shown = render(tree);

  assert.match(shown, new RegExp(FAILURE_TITLE), 'the title says the app could not start');
  assert.match(shown, /wiring the app/, 'and which step failed');
  assert.match(shown, /Unable to resolve module/, 'and the error itself');
  assert.match(shown, new RegExp(COPY_DETAILS), 'and the line a reviewer copies');
  assert.match(shown, new RegExp(FAILURE_HINT.slice(0, 30)), 'and what this screen is for');
  // The copy line is selectable -- that is the only mechanism the app has
  // without a clipboard dependency, and a screen the reviewer cannot copy from
  // is a screen they cannot report.
  const copy = nodesOfType(tree, FAILURE.COPY);
  assert.equal(copy.length, 1, 'there is exactly one copy line');
  assert.equal(copy[0].props.selectable, true, 'the copy line is selectable, so a long press copies it');
  assert.match(copy[0].children[0], /Unable to resolve module/, 'the copyable text carries the details');
  assert.match(copy[0].children[0], new RegExp(COPY_DETAILS), 'and says what it is, so a pasted report explains itself');
  assert.equal(copy[0].children[0].includes('\n'), true, 'and they are several lines: name, message and stack');
  // Mutation: move the details out of the selectable node and this is red, and
  // so is the tree's own account of itself.
  assert.deepEqual(
    Object.values(FAILURE).sort(),
    ['fugaro.failure', 'fugaro.failure.copy', 'fugaro.failure.hint', 'fugaro.failure.message', 'fugaro.failure.title', 'fugaro.failure.what'].sort(),
  );
  assert.equal(nodesOfType(tree, FAILURE.ROOT).length, 1, 'the screen has one root');
  assert.equal(nodesOfType(tree, FAILURE.MESSAGE).length, 1, 'and one message line');
});

test('the failure screen renders no secret and no account: a tree ends up in a screenshot', () => {
  // The shell's rule (`src/app/root.js`) applies here too, and more sharply: a
  // startup failure is exactly the moment somebody sends the screen to someone
  // else. Nothing is added to the details beyond the error the app threw.
  const React = fakeReact();
  const error = new Error('boom');
  error.firebaseProjectId = 'fugaro-dev';
  const tree = createFailureScreen({ React, View, Text, error }) ({});
  const shown = render(tree);
  assert.match(shown, /boom/);
  assert.equal(shown.includes('fugaro-dev'), false, 'the screen shows no field of an installation');
  assert.equal(shown.includes('idToken'), false);
  // ... and the error's own extra properties are not serialised wholesale: only
  // the name, the message and the stack are, which are the app's words.
  assert.equal(failureDetails(error).text.includes('firebaseProjectId'), false);
});

// --- the error boundary -----------------------------------------------------

test('the boundary renders the app until a render throws, then the failure screen', () => {
  const React = fakeReact();
  const fallback = (error) => React.createElement('failure-screen', { error });
  const Boundary = createErrorBoundary({ React, fallback });

  // No failure: the children are what render. Mutation: return the fallback
  // unconditionally and this is red.
  const healthy = new Boundary({ children: 'the app' });
  healthy.state = null;
  assert.equal(healthy.render(), 'the app');

  // A render throw below the boundary becomes `state.error`, and the fallback
  // is what renders. This is the one thing React cannot do with a hook.
  const error = new Error('Unknown prop on Text');
  const derived = Boundary.getDerivedStateFromError(error);
  assert.deepEqual(derived, { error });
  const failed = new Boundary({ children: 'the app' });
  failed.state = derived;
  const recovered = failed.render();
  assert.equal(recovered.type, 'failure-screen');
  assert.equal(recovered.props.error, error);

  // The fallback is the caller's, so the screen the reviewer sees is the same
  // screen a wiring failure produces -- one screen, not two.
  assert.equal(typeof Boundary.prototype.componentDidCatch, 'function');
  assert.equal(Boundary.prototype.componentDidCatch.call(failed, error), undefined, 'catching is not logging');
});

test('a boundary with no fallback, or with something that is not React, is refused', () => {
  const React = fakeReact();
  for (const bad of [undefined, null, 'fallback']) {
    assert.throws(() => createErrorBoundary({ React, fallback: bad }), /fallback/);
  }
  assert.throws(() => createErrorBoundary({ React: { createElement() {} }, fallback: () => null }), /Component/);
  assert.throws(() => createErrorBoundary({ React: { Component: class {} }, fallback: () => null }), /createElement/);
});

// --- the guard --------------------------------------------------------------

/** A guard wired the way `mobile/index.js` wires it. */
function guardFor() {
  const React = fakeReact();
  return { React, guard: createGuardedStartup({ React, View, Text }) };
}

test('a wiring throw renders the failure screen instead of closing the app', () => {
  // THE case this task exists for: `mobile/index.js` builds the Keychain port
  // at module scope, so a throw there used to be an app that opens and dies.
  const { React, guard } = guardFor();
  const Root = guard.root(() => {
    throw new TypeError('expo-secure-store module is missing WHEN_UNLOCKED_THIS_DEVICE_ONLY');
  });
  assert.equal(guard.state().failed, true, 'the failure is the guard\'s own state, so it can be shown later too');
  const instance = new Root({});
  const shown = render(instance.render());
  assert.match(shown, new RegExp(FAILURE_TITLE));
  assert.match(shown, /WHEN_UNLOCKED_THIS_DEVICE_ONLY/, 'the reviewer sees the real message on the phone');
  // ... and it is THAT screen: the same factory the boundary renders, so a
  // wiring failure and a render failure cannot drift into two screens.
  assert.equal(nodesOfType(instance.render(), FAILURE.ROOT).length, 1);
  assert.match(render(createFailureScreen({ React, View, Text, error: guard.state().error }) ({})), /WHEN_UNLOCKED_THIS_DEVICE_ONLY/);
});

test('a build that throws a non-Error still renders a screen and never throws out of root()', () => {
  const { guard } = guardFor();
  let Root = null;
  assert.doesNotThrow(() => {
    Root = guard.root(() => {
      throw 'the SDK is not linked';
    });
  }, 'the guard is a fence: it may not have a failure mode of its own');
  assert.equal(guard.state().failed, true);
  assert.match(render(new Root({}).render()), /the SDK is not linked/);
});

test('a healthy build renders the app inside the boundary', () => {
  const { React, guard } = guardFor();
  const App = function App() {
    return React.createElement('app', null);
  };
  const Root = guard.root(() => App);
  assert.equal(guard.state().failed, false);
  const tree = new Root({}).render();
  // The app is under the boundary, so a later render throw has somewhere to go.
  const Boundary = createErrorBoundary({ React, fallback: () => null });
  assert.equal(typeof tree.type, 'function', 'the boundary is a class component');
  assert.equal(typeof tree.type.getDerivedStateFromError, 'function', 'and a real error boundary');
  assert.equal(tree.children[0].type, App, 'and the app is what it wraps');
  // The two boundaries are built the same way, so this is a shape assertion and
  // not an identity one: same prototype chain, same static.
  assert.equal(
    Object.getPrototypeOf(tree.type.prototype).constructor.name,
    Object.getPrototypeOf(Boundary.prototype).constructor.name,
  );
});

test('a build that returns nothing usable is a reported failure, not a blank screen', () => {
  // A `build` that returns `undefined` would otherwise render nothing at all --
  // the same white screen, from a bug in the entry rather than in a port.
  const { guard } = guardFor();
  const Root = guard.root(() => undefined);
  assert.equal(guard.state().failed, true);
  assert.match(render(new Root({}).render()), new RegExp(FAILURE_TITLE));
  assert.match(guard.state().error.message, /root component/);
});

test('report() is the door for an async startup step, and the root re-renders itself when it fires', () => {
  // The wiring is synchronous and the app's first async work is a tap today
  // (`mobile/index.js` guards a rejected demo entry); a SecureStore read, a
  // Firebase init or a fetch will be the next one. Whatever it is, it arrives
  // here and not as an unhandled rejection nobody on the phone can see.
  //
  // **React's own path, executed rather than assumed.** The wiring from the
  // report to the screen is `componentDidMount` -> `subscribe` -> the guard's
  // `publish` -> this root's listener -> `setState` -> `render`. A test that
  // hand-assigned `instance.state` would assert the last link only, so it would
  // stay green if the subscription were never made, made with the wrong
  // callback, or dropped before the report. This drives the real chain: mount
  // the root the way React does, report, and read the tree the root then
  // renders -- with no line of the test writing to `state`.
  const { React, guard } = guardFor();
  const App = function App() {
    return React.createElement('app', null);
  };
  const Root = guard.root(() => App);
  const instance = new Root({});
  let notified = 0;
  const listener = guard.subscribe(() => {
    notified += 1;
  });
  assert.equal(render(instance.render()).includes(FAILURE_TITLE), false, 'healthy until something is reported');

  instance.componentDidMount();
  assert.equal(typeof instance.unsubscribe, 'function', 'mounting is what subscribes the root');
  // Before the report, the mounted root still renders the app (and the app
  // alone: a guard that subscribed itself into a failed state would be red
  // here).
  assert.equal(render(instance.render()).includes(FAILURE_TITLE), false);

  guard.report(new Error('AsyncStorage read failed during startup'));
  assert.equal(notified, 1, 'the subscribers are told once');
  // THE assertion this test exists for: nothing below wrote `instance.state`,
  // so the failure screen can only be there because the subscription moved it.
  const shown = render(instance.render());
  assert.match(shown, new RegExp(FAILURE_TITLE));
  assert.match(shown, /AsyncStorage read failed during startup/);
  assert.match(shown, new RegExp(COPY_DETAILS));
  assert.equal(instance.state.failed, true, 'the state the listener set is the guard\'s own');
  assert.equal(instance.state.error.message, 'AsyncStorage read failed during startup');

  // A second report is dropped (the first failure wins, asserted on its own
  // below), so there is nothing more to publish here: the unmount half is the
  // next test, which uses a guard whose report is the first one.
  // Mutation: make `componentDidMount` a no-op and the failure screen never
  // renders above.
  listener();
});

test('an unmounted root stops listening: a later failure cannot move a dead tree', () => {
  // React unmounts the tree on a reload, and a listener left behind would hold a
  // dead root and re-render into nothing. The publish below really happens
  // (`notified` moves, and the guard is in the failed state), so the assertion
  // is about the root's own subscription and not about a report that never
  // fired -- which is what made the version of this check inside the previous
  // test vacuous.
  const { React, guard } = guardFor();
  const App = function App() {
    return React.createElement('app', null);
  };
  const Root = guard.root(() => App);
  const instance = new Root({});
  let notified = 0;
  const listener = guard.subscribe(() => {
    notified += 1;
  });
  instance.componentDidMount();
  assert.equal(typeof instance.unsubscribe, 'function', 'mounting subscribes');

  instance.componentWillUnmount();
  assert.equal(instance.unsubscribe, null, 'unmounting drops the subscription');
  guard.report(new Error('the phone went away mid-launch'));
  assert.equal(notified, 1, 'the report was published, so this test is not vacuous');
  assert.equal(guard.state().failed, true, 'and the guard is in the failed state');
  assert.equal(instance.state.failed, false, 'but the unmounted root was not told');
  assert.equal(
    render(instance.render()).includes(FAILURE_TITLE),
    false,
    'so it still renders the app rather than a screen for a tree that no longer exists',
  );
  listener();
  // Mutation: drop the unsubscribe call in `componentWillUnmount` and the dead
  // root moves to the failure screen with everyone else.
});

// --- guard(): a step the app runs later, sync or async ------------------------

test('a synchronous throw out of a guarded step is reported, not rethrown', async () => {
  // THE half that used to escape. `mobile/index.js` hands this to a
  // `Pressable`'s `onPress`, which React calls from the platform's event loop --
  // outside the wiring's `try` and outside every boundary -- so a synchronous
  // throw there was an uncaught error in a release build: no screen, no report,
  // the exact failure this file exists to prevent. And it is the likelier half,
  // because a controller validates before it awaits.
  const { React, guard } = guardFor();
  const App = function App() {
    return React.createElement('app', null);
  };
  const Root = guard.root(() => App);
  const instance = new Root({});
  instance.componentDidMount();

  const onPress = guard.guard('entering the demo', () => {
    throw new Error('the demo refuses an empty command');
  });
  let answer;
  assert.doesNotThrow(() => {
    answer = onPress();
  }, 'a guarded step never throws at its caller: that caller is the platform');
  assert.equal(answer, undefined, 'a step that did not run answers nothing');
  assert.equal(guard.state().failed, true);
  const shown = render(instance.render());
  assert.match(shown, new RegExp(FAILURE_TITLE));
  assert.match(shown, /the demo refuses an empty command/);
  // The step is named, so the screen says which control failed and not only
  // what the error said.
  assert.match(guard.state().what, /entering the demo/);
  // Mutation: drop the `try` inside `guard` and this goes red with the throw
  // arriving at the caller -- which in the app is React, not a screen.
});

test('a rejected guarded step is reported too, and a healthy one is untouched', async () => {
  const { React, guard } = guardFor();
  const onTap = guard.guard('confirming the demo command', async () => {
    throw new Error('the queue refused the command');
  });
  // A rejection is the async half: the `try` cannot see it, so the promise is
  // handed to the same door. The guarded step returns **the same promise**, so a
  // caller that awaits still sees the rejection -- that is normal promise
  // semantics and the caller's business -- but the rejection is no longer
  // unhandled (a handler is attached), and the *report* is what reaches the
  // screen. In the app the caller is React's event loop, which awaits nothing.
  const answer = onTap();
  assert.equal(typeof answer.then, 'function', 'the guarded step still returns what it returned');
  let stillRejects = null;
  await answer.catch((error) => {
    stillRejects = error;
  });
  assert.match(stillRejects.message, /the queue refused the command/, 'the caller still sees the rejection');
  assert.equal(guard.state().failed, true, 'and the guard has it too, so the screen can show it');
  assert.match(guard.state().error.message, /the queue refused the command/);
  assert.match(guard.state().what, /confirming the demo command/);

  // A step that succeeds is not a failure: nothing is reported, and the answer
  // comes back unchanged so the caller keeps working.
  const { guard: healthy } = guardFor();
  const good = healthy.guard('the demo', async () => 'entered');
  assert.equal(await good(), 'entered');
  assert.equal(healthy.state().failed, false, 'a healthy step reports nothing');
  const plain = healthy.guard('the demo', () => 7);
  assert.equal(plain(), 7, 'a synchronous answer passes straight through');
  assert.equal(healthy.state().failed, false);

  // And a caller that hands it something that is not work is a wiring bug, said
  // out loud rather than discovered on a tap.
  assert.throws(() => healthy.guard('the demo'), /work/);
  assert.throws(() => healthy.guard('the demo', 'not a function'), /work/);
});

test('the guard keeps the FIRST failure: a cascade is the same report, not a new one', () => {
  // A startup failure usually arrives twice (the throw, then whatever the
  // caller does next). Reporting the first keeps the screen pointing at the
  // cause rather than at its consequence.
  const { guard } = guardFor();
  guard.root(() => {
    throw new Error('the cause');
  });
  const first = guard.state().error;
  guard.report(new Error('the consequence'));
  assert.equal(guard.state().error, first);
  assert.equal(guard.state().error.message, 'the cause');
  // Mutation: `report` overwriting unconditionally and this is red.
  guard.report(new Error('another consequence'));
  assert.equal(guard.state().error.message, 'the cause');
});

test('the guard renders the failure screen when the app itself throws while rendering', () => {
  // The render-time half: the boundary is inside the returned root, so a screen
  // that throws on its first render is caught. Asserted through the boundary's
  // own static, since the fake React is not a renderer.
  const { React, guard } = guardFor();
  const Root = guard.root(() => function App() {
    throw new Error('the first screen threw');
  });
  const instance = new Root({});
  const tree = instance.render();
  const Boundary = tree.type;
  const error = new Error('the first screen threw');
  // What React does with a render throw: `getDerivedStateFromError`, then the
  // fallback -- which is the same screen `report` renders.
  const recovered = new Boundary({ children: tree.children });
  recovered.state = Boundary.getDerivedStateFromError(error);
  const shown = render(recovered.render());
  assert.match(shown, new RegExp(FAILURE_TITLE));
  assert.match(shown, /the first screen threw/);
  assert.match(shown, new RegExp(COPY_DETAILS));
});
