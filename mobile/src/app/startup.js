// The guarded startup and the root error boundary (mobile plan, Task P3.1's
// shell, extended 2026-10-21 after the Android launch crash).
//
// **Why this file exists.** On 2026-10-21 the owner installed the `preview` APK
// on Android and it *opened and closed*: no screen, no error, nothing to read.
// The cause was a `TypeError` thrown while `mobile/index.js` was still being
// **evaluated** -- `expoSecureStorePort` required an `expo-secure-store`
// constant that does not exist on Android, and the port is built at module
// scope, before `registerRootComponent` and therefore before React ever
// renders. The cause is fixed where it was
// (`src/accounts/expo-secure-store.js`); this file is the answer to the *shape*
// of that failure, which is the half that will recur.
//
// A release build on a phone has no terminal. `console.log` goes nowhere a
// reviewer can look, React's red box is a development-only mechanism, and an
// exception during module evaluation is a silent exit. So the next startup
// failure -- a secure-store read that rejects, a Firebase init that returns
// nothing, an endpoint that is absent from a preview build, a renamed SDK
// export -- must arrive on the **screen**, in words, where the person holding
// the phone can read it and copy it into a message.
//
// **Two mechanisms, because there are two ways to fail.**
//
//   * `createGuardedStartup` wraps the **wiring**. `mobile/index.js` passes the
//     whole of it -- the two ports, the account store, the demo controller, the
//     screens and the shell -- to `root(build)`, and a throw from any of them
//     becomes a report instead of a crash. The same object exposes `report`,
//     which is the door for a failure that arrives **later**: an async startup
//     step cannot be caught by a `try` around synchronous code, and today the
//     app's first async work is a reviewer's tap on "Try the demo". A future
//     SecureStore read, Firebase init or fetch joins by holding this object.
//   * `createErrorBoundary` catches a **render-time** throw anywhere below the
//     root. React has no hook for this and no other mechanism: an error
//     boundary must be a class component with `getDerivedStateFromError`, which
//     is why React is taken here as an argument that must carry `Component`
//     (the shell takes only `createElement` -- `src/app/root.js` says why).
//
// Both render **one** screen. A reviewer who reports a startup failure and a
// reviewer who reports a screen that threw while rendering describe the same
// thing, so a second screen would be a second thing to keep true.
//
// **What the screen is allowed to say.** The same rule the shell lives by
// (`src/app/root.js`): no secret, no Firebase project, no operator uid. A
// startup-failure screen is the single most likely thing to be photographed and
// sent to somebody, so it carries the app's own words -- the error's name, its
// message and its stack -- and nothing from an installation. The stack is
// included deliberately: it is the difference between "it did not work" and a
// line number, and no value of D111's two items can reach it, because nothing in
// this tree puts one into an error (`secret.js` refuses to be printed at all).
//
// **The "Copy details" line, and why it is a `Text` and not a button.** The app
// has no clipboard module and does not gain one for this: a React Native `Text`
// with `selectable: true` is long-pressable and copies through the platform's
// own menu, which is the whole mechanism, works on both platforms, adds no
// dependency a build would have to link, and needs no permission. A `Pressable`
// labelled "Copy" that copied nothing would be worse than no button.

/** The element types this file uses, in one place so a test can name them too. */
export const FAILURE = Object.freeze({
  ROOT: 'fugaro.failure',
  TITLE: 'fugaro.failure.title',
  WHAT: 'fugaro.failure.what',
  MESSAGE: 'fugaro.failure.message',
  COPY: 'fugaro.failure.copy',
  HINT: 'fugaro.failure.hint',
});

/** The first line a reviewer reads. It says what happened, in the app's voice. */
export const FAILURE_TITLE = 'Fugaro could not start';

/**
 * What the screen is, so a reviewer knows not to keep tapping: the app is not
 * hung, it is showing them the reason it stopped.
 */
export const FAILURE_HINT =
  'This screen is what the app shows instead of closing. Nothing was sent anywhere and no account was touched. Long-press the details below to copy them.';

/** The label of the selectable line: what the reviewer does with it. */
export const COPY_DETAILS = 'Copy details';

/** What a stage is called when the caller does not say (it always may). */
const UNKNOWN_STAGE = 'startup';

/**
 * A value thrown by a startup, as the sentence the screen shows.
 *
 * **Anything can be thrown** -- a string, `undefined`, a plain object, a
 * circular one -- so this function may not have a failure mode of its own: it
 * is the fence around the failure, and a fence that throws while describing a
 * throw is the white screen it was written to prevent. `JSON.stringify` alone is
 * not enough (it throws on a circular value, and answers `undefined` for a
 * function), so each shape is named.
 */
function describeThrown(thrown) {
  if (thrown instanceof Error) {
    return { name: thrown.name || 'Error', message: thrown.message || '(no message)' };
  }
  if (typeof thrown === 'string') return { name: 'Thrown string', message: thrown };
  if (thrown === null) return { name: 'Thrown value', message: 'null' };
  if (thrown === undefined) return { name: 'Thrown value', message: 'undefined' };
  if (typeof thrown === 'object') {
    let message;
    try {
      message = JSON.stringify(thrown);
    } catch {
      // A circular value: `JSON.stringify` refuses it, and the useful part of a
      // thrown object is usually its keys, so say that instead.
      message = `[circular object with keys: ${Object.keys(thrown).join(', ') || 'none'}]`;
    }
    return { name: 'Thrown object', message: message ?? String(thrown) };
  }
  return { name: 'Thrown value', message: String(thrown) };
}

/**
 * A thrown value as the record the screen renders: which step failed, what was
 * thrown, and one copyable multi-line block for a bug report.
 *
 * @param {unknown} thrown whatever the startup threw
 * @param {string} [what] which step it was, in words ("wiring the app",
 *   "entering the demo"); the screen shows it, so it is a sentence and not a
 *   code
 * @returns {{ what: string, name: string, message: string, text: string }} a
 *   frozen record; `text` is what the reviewer copies
 */
export function failureDetails(thrown, what = UNKNOWN_STAGE) {
  const { name, message } = describeThrown(thrown);
  const stack = thrown instanceof Error && typeof thrown.stack === 'string' ? thrown.stack : null;
  const stage = typeof what === 'string' && what.length > 0 ? what : UNKNOWN_STAGE;
  const lines = [
    'Fugaro startup failure',
    `step: ${stage}`,
    `thrown: ${name}`,
    `message: ${message}`,
    stack === null ? 'stack: (none)' : `stack:\n${stack}`,
  ];
  return Object.freeze({ what: stage, name, message, text: lines.join('\n') });
}

/**
 * The plain screen a failed startup shows.
 *
 * It is a function of the three primitives it uses and nothing else: no
 * platform module, no hook, no state. A screen that needed a device to render
 * would be a screen that fails in exactly the situation it exists for.
 *
 * @param {object} options
 * @param {object} options.React React, with `createElement`
 * @param {Function} options.View react-native's View
 * @param {Function} options.Text react-native's Text
 * @param {unknown} options.error the thrown value
 * @param {string} [options.what] which step failed (see `failureDetails`)
 * @returns {Function} the screen, as a component taking no props
 */
export function createFailureScreen({ React, View, Text, error, what }) {
  if (React === null || typeof React !== 'object' || typeof React.createElement !== 'function') {
    throw new TypeError('createFailureScreen needs React (with createElement)');
  }
  for (const [name, part] of [['View', View], ['Text', Text]]) {
    if (typeof part !== 'function' && typeof part !== 'object') {
      throw new TypeError(`createFailureScreen needs react-native's ${name}`);
    }
  }
  // Described once, here, rather than on every render: the record is a value,
  // and a render that recomputed it could describe a different thing than the
  // one that was caught.
  const details = failureDetails(error, what);

  return function FailureScreen() {
    return React.createElement(
      View,
      { type: FAILURE.ROOT },
      React.createElement(Text, { type: FAILURE.TITLE }, FAILURE_TITLE),
      React.createElement(Text, { type: FAILURE.WHAT }, `While ${details.what}: ${details.name}`),
      React.createElement(Text, { type: FAILURE.MESSAGE, selectable: true }, details.message),
      // The copy line. `selectable` is the whole clipboard mechanism (see the
      // header): long-press, Copy, paste into a message.
      React.createElement(
        Text,
        { type: FAILURE.COPY, selectable: true },
        `${COPY_DETAILS}\n${details.text}`,
      ),
      React.createElement(Text, { type: FAILURE.HINT }, FAILURE_HINT),
    );
  };
}

/**
 * Build React's error boundary, over a fallback of the caller's choosing.
 *
 * A class component is not a style choice: `getDerivedStateFromError` is the
 * only mechanism React offers for catching a render-time throw, and it exists
 * on classes alone. `componentDidCatch` is defined because React calls it
 * anyway and a boundary that left it out would be a boundary whose behaviour
 * depends on which React minor is installed -- and it deliberately does
 * **nothing**: this tree has no logger (`src/boundary.test.js` refuses
 * `console.` anywhere in the app), and the screen is where the report goes.
 *
 * @param {object} options
 * @param {object} options.React React, with `createElement` and `Component`
 * @param {Function} options.fallback `(error) => element`, what renders after a
 *   throw
 * @returns {Function} the boundary class
 */
export function createErrorBoundary({ React, fallback }) {
  if (React === null || typeof React !== 'object' || typeof React.createElement !== 'function') {
    throw new TypeError('createErrorBoundary needs React (with createElement)');
  }
  if (typeof React.Component !== 'function') {
    throw new TypeError('createErrorBoundary needs React.Component: an error boundary is a class');
  }
  if (typeof fallback !== 'function') {
    throw new TypeError('createErrorBoundary needs a fallback: what renders after a render throw');
  }

  return class ErrorBoundary extends React.Component {
    /** What React calls with the thrown value; the state is the fallback's cue. */
    static getDerivedStateFromError(error) {
      return { error };
    }

    /** Called by React after a throw. There is nothing to do here on purpose. */
    componentDidCatch() {}

    render() {
      // `state` may be null (React's own initial state) or carry the error.
      if (this.state !== null && this.state !== undefined && this.state.error !== undefined) {
        return fallback(this.state.error);
      }
      return this.props?.children ?? null;
    }
  };
}

/**
 * The guarded startup: build the app, and if building it throws, show why.
 *
 * **The order is the property.** `root(build)` calls `build()` **inside** the
 * guard, so a throw from the wiring is reported rather than propagated out of
 * `mobile/index.js` -- which is the difference between the screen this file
 * draws and the app that closes. The returned component subscribes to the
 * guard, so a failure reported **after** a successful start (an async step)
 * re-renders into the same screen.
 *
 * @param {object} options
 * @param {object} options.React React, with `createElement` and `Component`
 * @param {Function} options.View react-native's View
 * @param {Function} options.Text react-native's Text
 * @returns {object} the frozen guard: `state()`, `subscribe(listener)`,
 *   `report(error, what)`, `guard(what, run)`, `root(build)`
 */
export function createGuardedStartup({ React, View, Text }) {
  // The screen's own validation runs here, at build time, so a caller that
  // handed this the wrong primitives hears about it before a phone does.
  createFailureScreen({ React, View, Text, error: null });

  /**
   * The failure, or the absence of one. One value at a time, the **first** one:
   * a startup failure usually arrives twice -- the throw, and whatever the
   * caller does next -- and a screen that showed the second would be pointing
   * at the consequence instead of the cause.
   */
  let snapshot = Object.freeze({ failed: false, error: null });

  /** Everything the guard remembers, as a stable value between changes. */
  function state() {
    return snapshot;
  }

  /** The subscription React's `setState` rides on, and the demo's own shape. */
  const listeners = new Set();
  function subscribe(listener) {
    if (typeof listener !== 'function') {
      throw new TypeError('the startup guard\'s subscribe: a listener function is required');
    }
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  }

  function publish(next) {
    snapshot = Object.freeze(next);
    // A listener that throws does not stop the others: this is a screen's
    // re-render, and one broken subscriber must not take the report with it
    // (the same rule the demo controller's `publish` keeps).
    for (const listener of [...listeners]) {
      try {
        listener();
      } catch {
        // deliberately swallowed: see above
      }
    }
  }

  /**
   * Show a failure. This is the door for anything the wiring's `try` cannot
   * cover: a rejected promise from an async startup step (`mobile/index.js`
   * reports the demo's own `enter()` through it), and anything a later task
   * adds. The first report wins (see `snapshot`); a second is dropped.
   *
   * @param {unknown} error whatever was thrown or rejected
   * @param {string} [what] which step failed, in words
   * @returns {object} the guard's state, which is now the failure
   */
  function report(error, what = UNKNOWN_STAGE) {
    if (!snapshot.failed) publish({ failed: true, error, what });
    return snapshot;
  }

  /**
   * Wrap one step the app runs **later** -- a tap handler, an async startup
   * step, anything a `try` around the wiring cannot reach -- so that its failure
   * becomes a report rather than an uncaught error.
   *
   * **Both halves, because a handler is where a throw is easiest to lose.** A
   * tap handler runs from the platform's event loop, not from `root`'s `try`, so
   * a **synchronous** throw out of `run()` is an uncaught error in a release
   * build -- exactly the invisible failure this file exists for -- and the
   * synchronous half is the likelier one in practice, because a controller
   * usually validates before it awaits. The returned answer of a synchronous
   * failure is `undefined` rather than a rethrow: the control did not do the
   * thing, and the screen says why.
   *
   * @param {string} [what] which step it is, in words (see `failureDetails`)
   * @param {Function} run the work, sync or async
   * @returns {Function} `() => unknown`: the same work, guarded
   */
  function guard(what, run) {
    if (typeof run !== 'function') {
      throw new TypeError('the startup guard\'s guard needs the work to run');
    }
    return function guarded() {
      let answer;
      try {
        answer = run();
      } catch (error) {
        report(error, what);
        return undefined;
      }
      // A thenable is the async half: a rejection cannot be caught by the `try`
      // above, so it is handed to the same door.
      if (
        answer !== null &&
        typeof answer === 'object' &&
        typeof answer.then === 'function' &&
        typeof answer.catch === 'function'
      ) {
        answer.catch((error) => report(error, what));
      }
      return answer;
    };
  }

  /** The screen for a failure, built fresh so the boundary's fallback and the
   * guard's own render show the same thing. */
  function failureScreen(error, what) {
    const Screen = createFailureScreen({ React, View, Text, error, what });
    return React.createElement(Screen, { key: 'failure' });
  }

  // The boundary's fallback is the failure screen: a render throw below the
  // root and a throw while wiring land on one screen, so there is one thing to
  // read, one thing to fix and one thing to keep true.
  const Boundary = createErrorBoundary({ React, fallback: (error) => failureScreen(error, 'rendering the screen') });

  /**
   * Build the app's root component, guarded.
   *
   * @param {Function} build `() => component`: the app's own root. Called here,
   *   inside the guard, so a throw from the wiring is a report.
   * @returns {Function} the root component: the app under the boundary, or the
   *   failure screen
   */
  function root(build) {
    if (typeof build !== 'function') {
      throw new TypeError('the startup guard\'s root needs what builds the app');
    }
    let App = null;
    try {
      App = build();
    } catch (error) {
      report(error, 'wiring the app');
    }
    // A `build` that answered with nothing usable is the same white screen from
    // a different bug, so it is a reported failure too rather than a render of
    // `undefined` -- and the message says which of the two it was.
    if (App === null || (typeof App !== 'function' && typeof App !== 'object')) {
      report(
        new TypeError(
          'the startup guard\'s build returned no root component, so there is nothing to render',
        ),
        'wiring the app',
      );
      App = null;
    }

    const Reported = App;

    return class GuardedRoot extends React.Component {
      constructor(props) {
        super(props);
        const current = state();
        this.state = { failed: current.failed, error: current.error, what: current.what };
        this.unsubscribe = null;
      }

      componentDidMount() {
        this.unsubscribe = subscribe(() => {
          const current = state();
          this.setState({ failed: current.failed, error: current.error, what: current.what });
        });
      }

      componentWillUnmount() {
        if (this.unsubscribe !== null) this.unsubscribe();
        this.unsubscribe = null;
      }

      render() {
        if (this.state.failed === true) return failureScreen(this.state.error, this.state.what);
        // `Reported` is never null here: a `build` that produced nothing has
        // already reported, so `this.state.failed` is true above.
        if (Reported === null) return null;
        return React.createElement(Boundary, null, React.createElement(Reported, null));
      }
    };
  }

  return Object.freeze({ ok: true, state, subscribe, report, guard, root });
}
